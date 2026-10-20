<script lang="ts">
import { defineComponent, ref, onMounted, computed } from 'vue';
import { useRoute } from 'vue-router';
import { useStore } from 'vuex';
import Loading from '@shell/components/Loading.vue';
import { TopologyApiService, GraphResponse } from '../services/api';
import { computeLayout, PositionedGraph, PositionedNode } from '../utils/layout';
import TopologyCanvas from '../components/TopologyCanvas.vue';
import NodeDetailDrawer from '../components/NodeDetailDrawer.vue';
import DaemonMissing from '../components/DaemonMissing.vue';

export default defineComponent({
  'name':       'TopologyPage',
  'components': {
    Loading,
    TopologyCanvas,
    NodeDetailDrawer,
    DaemonMissing,
  },
  setup() {
    const route = useRoute();
    const store = useStore();

    const clusterId = computed(() => (route.params.cluster as string) || 'local');
    const apiService = computed(() => new TopologyApiService(store, clusterId.value));

    const loading = ref(true);
    const daemonMissing = ref(false);
    const graphData = ref<GraphResponse | null>(null);
    const layout = ref<PositionedGraph | null>(null);
    const selectedNode = ref<PositionedNode | null>(null);
    const errorMessage = ref<string | null>(null);

    const loadTopology = async() => {
      loading.value = true;
      errorMessage.value = null;
      daemonMissing.value = false;

      try {
        // Step 1: Probe daemon version to verify availability
        await apiService.value.getVersion();

        // Step 2: Fetch L3 graph
        const res = await apiService.value.getGraph('l3');

        graphData.value = res;

        // Step 3: Compute compound layout with ELK
        layout.value = await computeLayout(res, clusterId.value);
      } catch (err: any) {
        if (err?.status === 404 || err?.status === 503 || err?.message?.includes('404')) {
          daemonMissing.value = true;
        } else {
          errorMessage.value = err?.message || 'Failed to load network topology from daemon';
        }
      } finally {
        loading.value = false;
      }
    };

    const onSelectNode = (node: PositionedNode) => {
      selectedNode.value = node;
    };

    const onCloseDrawer = () => {
      selectedNode.value = null;
    };

    onMounted(() => {
      loadTopology();
    });

    return {
      clusterId,
      apiService,
      loading,
      daemonMissing,
      graphData,
      layout,
      selectedNode,
      errorMessage,
      loadTopology,
      onSelectNode,
      onCloseDrawer,
    };
  },
});
</script>

<template>
  <div class="topology-page">
    <div class="topology-header">
      <div class="header-left">
        <h2>Network Topology</h2>
        <div
          v-if="graphData"
          class="header-badges"
        >
          <span class="badge revision">Rev {{ graphData.revision }}</span>
          <span class="badge stat">{{ graphData.nodes.length }} Nodes</span>
          <span class="badge stat">{{ graphData.edges.length }} Edges</span>
        </div>
      </div>
      <div class="header-right">
        <button
          class="btn btn-sm role-secondary"
          :disabled="loading"
          @click="loadTopology"
        >
          Refresh
        </button>
      </div>
    </div>

    <div class="topology-body">
      <Loading
        v-if="loading"
        mode="relative"
      />

      <DaemonMissing
        v-else-if="daemonMissing"
        :cluster-id="clusterId"
      />

      <div
        v-else-if="errorMessage"
        class="error-banner"
      >
        <p>{{ errorMessage }}</p>
      </div>

      <div
        v-else-if="layout"
        class="canvas-wrapper"
      >
        <TopologyCanvas
          :layout="layout"
          :selected-node-id="selectedNode?.id || null"
          @select-node="onSelectNode"
        />

        <NodeDetailDrawer
          v-if="selectedNode"
          :node="selectedNode"
          :api-service="apiService"
          @close="onCloseDrawer"
        />
      </div>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.topology-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 100%;

  .topology-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 24px;
    border-bottom: 1px solid var(--border);
    background-color: var(--box-bg);

    .header-left {
      display: flex;
      align-items: center;
      gap: 16px;

      h2 {
        margin: 0;
        font-size: 18px;
        color: var(--body-text);
      }

      .header-badges {
        display: flex;
        gap: 8px;

        .badge {
          padding: 2px 8px;
          border-radius: 4px;
          font-size: 11px;
          font-weight: 600;

          &.revision {
            background-color: var(--primary);
            color: #ffffff;
          }

          &.stat {
            background-color: var(--body-bg);
            border: 1px solid var(--border);
            color: var(--muted);
          }
        }
      }
    }
  }

  .topology-body {
    flex: 1;
    position: relative;
    overflow: hidden;

    .canvas-wrapper {
      width: 100%;
      height: 100%;
      position: relative;
    }

    .error-banner {
      padding: 24px;
      color: var(--error);
      text-align: center;
    }
  }
}
</style>
