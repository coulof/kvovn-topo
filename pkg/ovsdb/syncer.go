package ovsdb

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coulof/ovn-topology/pkg/graph"
	"github.com/coulof/ovn-topology/pkg/ovsdb/nbmodel"
	"github.com/coulof/ovn-topology/pkg/ovsdb/sbmodel"
	"github.com/ovn-kubernetes/libovsdb/cache"
	"github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/model"
)

type eventType int

const (
	eventAdd eventType = iota
	eventUpdate
	eventDelete
)

type dbEvent struct {
	table     string
	eventType eventType
	m         model.Model
}

// Syncer manages OVSDB monitors for NB and SB, maintaining an up-to-date graph store.
type Syncer struct {
	nbClient client.Client
	sbClient client.Client
	store    *graph.Store
	logger   *slog.Logger

	eventsChan chan dbEvent
	closeOnce  sync.Once
	stopCh     chan struct{}
}

// NewSyncer creates a Syncer connecting NB and SB monitors to the graph store.
func NewSyncer(nbClient, sbClient client.Client, store *graph.Store, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		nbClient:   nbClient,
		sbClient:   sbClient,
		store:      store,
		logger:     logger,
		eventsChan: make(chan dbEvent, 1024),
		stopCh:     make(chan struct{}),
	}
}

// Start connects to NB and SB, starts monitors, and blocks until ctx is canceled.
func (s *Syncer) Start(ctx context.Context) error {
	s.logger.Info("connecting to ovn databases",
		"nb_endpoints", s.nbClient.CurrentEndpoint(),
		"sb_endpoints", s.sbClient.CurrentEndpoint())

	// Connect NB
	if err := s.nbClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to OVN NB: %w", err)
	}
	// Connect SB
	if err := s.sbClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to OVN SB: %w", err)
	}

	// Register cache event handlers before starting monitors
	s.registerHandlers()

	// Start monitors
	if _, err := s.nbClient.MonitorAll(ctx); err != nil {
		return fmt.Errorf("failed to monitor OVN NB: %w", err)
	}
	if _, err := s.sbClient.MonitorAll(ctx); err != nil {
		return fmt.Errorf("failed to monitor OVN SB: %w", err)
	}

	// Perform initial full rebuild from monitor caches
	if err := s.rebuildFullSnapshot(ctx); err != nil {
		return fmt.Errorf("initial graph snapshot build failed: %w", err)
	}

	s.store.SetReady(true)
	s.logger.Info("initial ovn topology snapshot synchronized successfully")

	// Start event loop for incremental updates and disconnect handling
	go s.processEvents(ctx)
	go s.watchDisconnects(ctx)

	<-ctx.Done()
	s.Close()
	return ctx.Err()
}

// Close disconnects clients and shuts down event processing.
func (s *Syncer) Close() {
	s.closeOnce.Do(func() {
		close(s.stopCh)
		s.nbClient.Close()
		s.sbClient.Close()
	})
}

func (s *Syncer) registerHandlers() {
	nbHandler := &cache.EventHandlerFuncs{
		AddFunc: func(table string, m model.Model) {
			s.enqueueEvent(table, eventAdd, m)
		},
		UpdateFunc: func(table string, _, newModel model.Model) {
			s.enqueueEvent(table, eventUpdate, newModel)
		},
		DeleteFunc: func(table string, m model.Model) {
			s.enqueueEvent(table, eventDelete, m)
		},
	}
	s.nbClient.Cache().AddEventHandler(nbHandler)

	sbHandler := &cache.EventHandlerFuncs{
		AddFunc: func(table string, m model.Model) {
			s.enqueueEvent(table, eventAdd, m)
		},
		UpdateFunc: func(table string, _, newModel model.Model) {
			s.enqueueEvent(table, eventUpdate, newModel)
		},
		DeleteFunc: func(table string, m model.Model) {
			s.enqueueEvent(table, eventDelete, m)
		},
	}
	s.sbClient.Cache().AddEventHandler(sbHandler)
}

func (s *Syncer) enqueueEvent(table string, et eventType, m model.Model) {
	select {
	case s.eventsChan <- dbEvent{table: table, eventType: et, m: m}:
	default:
		s.logger.Warn("ovsdb event queue full, event dropped (will recover on resync)", "table", table)
	}
}

func (s *Syncer) processEvents(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	var batch []dbEvent

	flush := func() {
		if len(batch) == 0 {
			return
		}
		events := batch
		batch = nil

		s.store.Apply(func(draft *graph.Snapshot) []string {
			changedMap := make(map[string]struct{})
			for _, ev := range events {
				changedIDs := applyEventToDraft(draft, ev)
				for _, id := range changedIDs {
					changedMap[id] = struct{}{}
				}
			}
			result := make([]string, 0, len(changedMap))
			for id := range changedMap {
				result = append(result, id)
			}
			return result
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case ev := <-s.eventsChan:
			batch = append(batch, ev)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Syncer) watchDisconnects(ctx context.Context) {
	nbDisc := s.nbClient.DisconnectNotify()
	sbDisc := s.sbClient.DisconnectNotify()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-nbDisc:
			s.logger.Warn("disconnected from OVN NB, awaiting reconnect")
			s.store.SetReady(false)
			// Wait until reconnected then rebuild snapshot
			s.awaitReconnect(ctx)
		case <-sbDisc:
			s.logger.Warn("disconnected from OVN SB, awaiting reconnect")
			s.store.SetReady(false)
			s.awaitReconnect(ctx)
		}
	}
}

func (s *Syncer) awaitReconnect(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			if s.nbClient.Connected() && s.sbClient.Connected() {
				s.logger.Info("reconnected to OVN databases, rebuilding full snapshot")
				if err := s.rebuildFullSnapshot(ctx); err != nil {
					s.logger.Error("failed to rebuild snapshot on reconnect", "error", err)
					continue
				}
				s.store.SetReady(true)
				return
			}
		}
	}
}

func (s *Syncer) rebuildFullSnapshot(ctx context.Context) error {
	s.store.Apply(func(draft *graph.Snapshot) []string {
		// Clear existing NB & SB maps in draft
		draft.LogicalRouters = make(map[string]*nbmodel.LogicalRouter)
		draft.LogicalRouterPorts = make(map[string]*nbmodel.LogicalRouterPort)
		draft.StaticRoutes = make(map[string]*nbmodel.LogicalRouterStaticRoute)
		draft.Policies = make(map[string]*nbmodel.LogicalRouterPolicy)
		draft.NATs = make(map[string]*nbmodel.NAT)
		draft.GatewayChassis = make(map[string]*nbmodel.GatewayChassis)
		draft.HAChassisGroups = make(map[string]*nbmodel.HAChassisGroup)
		draft.HAChassis = make(map[string]*nbmodel.HAChassis)
		draft.LogicalSwitches = make(map[string]*nbmodel.LogicalSwitch)
		draft.LogicalSwitchPorts = make(map[string]*nbmodel.LogicalSwitchPort)
		draft.LoadBalancers = make(map[string]*nbmodel.LoadBalancer)
		draft.Chassis = make(map[string]*sbmodel.Chassis)
		draft.Encaps = make(map[string]*sbmodel.Encap)
		draft.PortBindings = make(map[string]*sbmodel.PortBinding)
		draft.DatapathBindings = make(map[string]*sbmodel.DatapathBinding)

		// Populate from NB cache
		var lrs []nbmodel.LogicalRouter
		if err := s.nbClient.List(ctx, &lrs); err == nil {
			for i := range lrs {
				draft.LogicalRouters[lrs[i].UUID] = &lrs[i]
			}
		}
		var lrps []nbmodel.LogicalRouterPort
		if err := s.nbClient.List(ctx, &lrps); err == nil {
			for i := range lrps {
				draft.LogicalRouterPorts[lrps[i].UUID] = &lrps[i]
			}
		}
		var routes []nbmodel.LogicalRouterStaticRoute
		if err := s.nbClient.List(ctx, &routes); err == nil {
			for i := range routes {
				draft.StaticRoutes[routes[i].UUID] = &routes[i]
			}
		}
		var policies []nbmodel.LogicalRouterPolicy
		if err := s.nbClient.List(ctx, &policies); err == nil {
			for i := range policies {
				draft.Policies[policies[i].UUID] = &policies[i]
			}
		}
		var nats []nbmodel.NAT
		if err := s.nbClient.List(ctx, &nats); err == nil {
			for i := range nats {
				draft.NATs[nats[i].UUID] = &nats[i]
			}
		}
		var gwChassis []nbmodel.GatewayChassis
		if err := s.nbClient.List(ctx, &gwChassis); err == nil {
			for i := range gwChassis {
				draft.GatewayChassis[gwChassis[i].UUID] = &gwChassis[i]
			}
		}
		var haGroups []nbmodel.HAChassisGroup
		if err := s.nbClient.List(ctx, &haGroups); err == nil {
			for i := range haGroups {
				draft.HAChassisGroups[haGroups[i].UUID] = &haGroups[i]
			}
		}
		var haChassis []nbmodel.HAChassis
		if err := s.nbClient.List(ctx, &haChassis); err == nil {
			for i := range haChassis {
				draft.HAChassis[haChassis[i].UUID] = &haChassis[i]
			}
		}
		var lss []nbmodel.LogicalSwitch
		if err := s.nbClient.List(ctx, &lss); err == nil {
			for i := range lss {
				draft.LogicalSwitches[lss[i].UUID] = &lss[i]
			}
		}
		var lsps []nbmodel.LogicalSwitchPort
		if err := s.nbClient.List(ctx, &lsps); err == nil {
			for i := range lsps {
				draft.LogicalSwitchPorts[lsps[i].UUID] = &lsps[i]
			}
		}
		var lbs []nbmodel.LoadBalancer
		if err := s.nbClient.List(ctx, &lbs); err == nil {
			for i := range lbs {
				draft.LoadBalancers[lbs[i].UUID] = &lbs[i]
			}
		}

		// Populate from SB cache
		var chassisList []sbmodel.Chassis
		if err := s.sbClient.List(ctx, &chassisList); err == nil {
			for i := range chassisList {
				draft.Chassis[chassisList[i].Name] = &chassisList[i]
			}
		}
		var encaps []sbmodel.Encap
		if err := s.sbClient.List(ctx, &encaps); err == nil {
			for i := range encaps {
				draft.Encaps[encaps[i].UUID] = &encaps[i]
			}
		}
		var pbs []sbmodel.PortBinding
		if err := s.sbClient.List(ctx, &pbs); err == nil {
			for i := range pbs {
				draft.PortBindings[pbs[i].UUID] = &pbs[i]
			}
		}
		var dps []sbmodel.DatapathBinding
		if err := s.sbClient.List(ctx, &dps); err == nil {
			for i := range dps {
				draft.DatapathBindings[dps[i].UUID] = &dps[i]
			}
		}

		return []string{"*"}
	})

	return nil
}

func applyEventToDraft(draft *graph.Snapshot, ev dbEvent) []string {
	var changed []string

	switch val := ev.m.(type) {
	case *nbmodel.LogicalRouter:
		if ev.eventType == eventDelete {
			delete(draft.LogicalRouters, val.UUID)
		} else {
			draft.LogicalRouters[val.UUID] = val
		}
		changed = append(changed, graph.PrefixLR+val.UUID)

	case *nbmodel.LogicalRouterPort:
		if ev.eventType == eventDelete {
			delete(draft.LogicalRouterPorts, val.UUID)
		} else {
			draft.LogicalRouterPorts[val.UUID] = val
		}
		changed = append(changed, graph.PrefixLRP+val.UUID)

	case *nbmodel.LogicalSwitch:
		if ev.eventType == eventDelete {
			delete(draft.LogicalSwitches, val.UUID)
		} else {
			draft.LogicalSwitches[val.UUID] = val
		}
		changed = append(changed, graph.PrefixLS+val.UUID)

	case *nbmodel.LogicalSwitchPort:
		if ev.eventType == eventDelete {
			delete(draft.LogicalSwitchPorts, val.UUID)
		} else {
			draft.LogicalSwitchPorts[val.UUID] = val
		}
		changed = append(changed, graph.PrefixLSP+val.UUID)

	case *nbmodel.NAT:
		if ev.eventType == eventDelete {
			delete(draft.NATs, val.UUID)
		} else {
			draft.NATs[val.UUID] = val
		}
		changed = append(changed, graph.PrefixNAT+val.UUID)

	case *sbmodel.Chassis:
		if ev.eventType == eventDelete {
			delete(draft.Chassis, val.Name)
		} else {
			draft.Chassis[val.Name] = val
		}
		changed = append(changed, graph.PrefixChassis+val.Name)

	case *sbmodel.PortBinding:
		if ev.eventType == eventDelete {
			delete(draft.PortBindings, val.UUID)
		} else {
			draft.PortBindings[val.UUID] = val
		}
		changed = append(changed, graph.PrefixLSP+val.LogicalPort)
	}

	return changed
}
