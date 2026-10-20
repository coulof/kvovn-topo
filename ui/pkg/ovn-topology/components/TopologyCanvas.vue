<script lang="ts">
import {
  defineComponent, ref, onMounted, PropType, watch
} from 'vue';
import * as d3 from 'd3';
import { PositionedGraph } from '../utils/layout';

export default defineComponent({
  'name':  'TopologyCanvas',
  'props': {
    'layout': {
      'type':     Object as PropType<PositionedGraph>,
      'required': true,
    },
    'selectedNodeId': {
      'type':    String as PropType<string | null>,
      'default': null,
    },
  },
  'emits': ['select-node'],
  setup(props) {
    const svgRef = ref<SVGSVGElement | null>(null);
    const viewportRef = ref<SVGGElement | null>(null);

    const initZoom = () => {
      if (!svgRef.value || !viewportRef.value) {
        return;
      }

      const zoom = d3.zoom<SVGSVGElement, unknown>()
        .scaleExtent([0.15, 3.5])
        .on('zoom', (event) => {
          if (viewportRef.value) {
            d3.select(viewportRef.value).attr('transform', event.transform.toString());
          }
        });

      d3.select(svgRef.value).call(zoom);

      // Initial center fit
      const initialScale = 0.85;
      const initialTransform = d3.zoomIdentity
        .translate(60, 40)
        .scale(initialScale);

      d3.select(svgRef.value).call(zoom.transform, initialTransform);
    };

    onMounted(() => {
      initZoom();
    });

    watch(() => props.layout, () => {
      initZoom();
    });

    return {
      svgRef,
      viewportRef,
    };
  },
});
</script>

<template>
  <div class="topology-canvas-container">
    <svg
      ref="svgRef"
      class="topology-svg"
    >
      <defs>
        <marker
          id="arrowhead"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="6"
          markerHeight="6"
          orient="auto-start-reverse"
        >
          <path
            d="M 0 1.5 L 10 5 L 0 8.5 z"
            class="arrowhead-path"
          />
        </marker>
      </defs>

      <g
        ref="viewportRef"
        class="viewport"
      >
        <!-- VPC Compound Background Boxes -->
        <g class="vpc-containers">
          <template
            v-for="node in layout.nodes"
            :key="'vpc-' + node.id"
          >
            <g
              v-if="node.isCompound || node.type === 'VPC'"
              class="vpc-group"
            >
              <rect
                :x="node.x"
                :y="node.y"
                :width="node.width"
                :height="node.height"
                rx="8"
                class="vpc-box"
              />
              <text
                :x="node.x + 14"
                :y="node.y + 22"
                class="vpc-title"
              >
                VPC: {{ node.name }}
              </text>
            </g>
          </template>
        </g>

        <!-- Edges (router-peer & localnet) -->
        <g class="edges">
          <g
            v-for="edge in layout.edges"
            :key="edge.id"
            class="edge-group"
          >
            <path
              :d="edge.pathD"
              class="edge-line"
              marker-end="url(#arrowhead)"
            />
            <g
              v-if="edge.label && edge.labelX && edge.labelY"
              class="edge-label-group"
            >
              <rect
                :x="edge.labelX - 42"
                :y="edge.labelY - 10"
                width="84"
                height="18"
                rx="4"
                class="edge-label-bg"
              />
              <text
                :x="edge.labelX"
                :y="edge.labelY + 3"
                class="edge-label-text"
              >
                {{ edge.label }}
              </text>
            </g>
          </g>
        </g>

        <!-- Leaf Nodes (Routers, Switches, External) -->
        <g class="nodes">
          <template
            v-for="node in layout.nodes"
            :key="node.id"
          >
            <g
              v-if="!node.isCompound && node.type !== 'VPC'"
              class="node-group"
              :class="{
                selected: node.id === selectedNodeId,
                'router-node': node.type === 'LR',
                'switch-node': node.type === 'LS',
                'external-node': node.type === 'External'
              }"
              :transform="`translate(${node.x}, ${node.y})`"
              @click="$emit('select-node', node)"
            >
              <rect
                :width="node.width"
                :height="node.height"
                rx="6"
                class="node-box"
              />

              <!-- Node Type Icon / Indicator Badge -->
              <rect
                :x="8"
                :y="8"
                width="22"
                height="16"
                rx="3"
                class="type-badge-rect"
              />
              <text
                :x="19"
                :y="19"
                class="type-badge-text"
              >
                {{ node.type === 'LR' ? 'LR' : node.type === 'LS' ? 'LS' : 'EXT' }}
              </text>

              <!-- Node Name -->
              <text
                :x="36"
                :y="20"
                class="node-title"
              >
                {{ node.name }}
              </text>

              <!-- Subtitle / Statistics -->
              <text
                v-if="node.type === 'LR'"
                :x="12"
                :y="45"
                class="node-subtitle"
              >
                Routes: {{ node.properties?.routeCount ?? 0 }} | NAT: {{ node.properties?.natCount ?? 0 }}
              </text>
              <text
                v-else-if="node.type === 'LS'"
                :x="12"
                :y="42"
                class="node-subtitle"
              >
                Ports: {{ node.properties?.portCount ?? 0 }} ({{ node.properties?.boundPortCount ?? 0 }} bound)
              </text>
            </g>
          </template>
        </g>
      </g>
    </svg>
  </div>
</template>

<style lang="scss" scoped>
.topology-canvas-container {
  width: 100%;
  height: 100%;
  position: relative;
  overflow: hidden;
  background-color: var(--body-bg);

  .topology-svg {
    width: 100%;
    height: 100%;
    cursor: grab;

    &:active {
      cursor: grabbing;
    }
  }

  // VPC Compound Container
  .vpc-box {
    fill: var(--box-bg);
    stroke: var(--border);
    stroke-width: 1.5px;
    stroke-dasharray: 4 2;
    opacity: 0.85;
  }

  .vpc-title {
    font-size: 13px;
    font-weight: 600;
    fill: var(--body-text);
  }

  // Edges
  .edge-line {
    fill: none;
    stroke: var(--border);
    stroke-width: 1.75px;
  }

  .arrowhead-path {
    fill: var(--border);
  }

  .edge-label-bg {
    fill: var(--box-bg);
    stroke: var(--subtle-border, var(--border));
    stroke-width: 1px;
  }

  .edge-label-text {
    font-size: 10px;
    font-weight: 500;
    fill: var(--primary);
    text-anchor: middle;
    font-family: monospace;
  }

  // Nodes
  .node-group {
    cursor: pointer;
    transition: transform 0.15s ease;

    .node-box {
      fill: var(--box-bg);
      stroke: var(--border);
      stroke-width: 1.5px;
      transition: stroke 0.2s, stroke-width 0.2s;
    }

    .type-badge-rect {
      fill: var(--primary);
      opacity: 0.15;
    }

    .type-badge-text {
      font-size: 9px;
      font-weight: 700;
      fill: var(--primary);
      text-anchor: middle;
    }

    .node-title {
      font-size: 12px;
      font-weight: 600;
      fill: var(--body-text);
    }

    .node-subtitle {
      font-size: 10px;
      fill: var(--muted);
      font-family: monospace;
    }

    &:hover .node-box {
      stroke: var(--primary);
      stroke-width: 2px;
    }

    &.selected .node-box {
      stroke: var(--primary);
      stroke-width: 2.5px;
    }

    &.router-node .type-badge-text {
      fill: var(--primary);
    }

    &.switch-node .type-badge-text {
      fill: var(--success);
    }

    &.switch-node .type-badge-rect {
      fill: var(--success);
    }

    &.external-node .type-badge-text {
      fill: var(--warning);
    }
  }
}
</style>
