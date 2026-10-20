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

      // Auto-fit and center graph within container
      const containerW = svgRef.value.clientWidth || 1000;
      const containerH = svgRef.value.clientHeight || 700;
      const graphW = props.layout.bounds.width || 1000;
      const graphH = props.layout.bounds.height || 700;

      const scale = Math.min((containerW - 80) / graphW, (containerH - 80) / graphH, 1.0);
      const finalScale = Math.max(scale, 0.45);
      const tx = Math.max((containerW - graphW * finalScale) / 2, 40);
      const ty = Math.max((containerH - graphH * finalScale) / 2, 40);

      const initialTransform = d3.zoomIdentity
        .translate(tx, ty)
        .scale(finalScale);

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
                rx="10"
                class="vpc-box"
              />
              <rect
                :x="node.x + 14"
                :y="node.y + 10"
                :width="Math.max(node.name.length * 8 + 48, 90)"
                height="22"
                rx="4"
                class="vpc-header-bg"
              />
              <text
                :x="node.x + 22"
                :y="node.y + 25"
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
                :x="edge.labelX - (edge.label.length * 4.2 + 14)"
                :y="edge.labelY - 11"
                :width="(edge.label.length * 8.4 + 28)"
                height="22"
                rx="11"
                class="edge-label-bg"
              />
              <text
                :x="edge.labelX"
                :y="edge.labelY + 4"
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
                rx="8"
                class="node-box"
              />

              <!-- Top Accent Color Bar -->
              <path
                :d="`M 0 8 A 8 8 0 0 1 8 0 L ${node.width - 8} 0 A 8 8 0 0 1 ${node.width} 8 L ${node.width} 8 L 0 8 Z`"
                class="node-accent-bar"
              />

              <!-- Node Type Icon / Indicator Badge -->
              <rect
                :x="12"
                :y="16"
                width="28"
                height="18"
                rx="4"
                class="type-badge-rect"
              />
              <text
                :x="26"
                :y="29"
                class="type-badge-text"
              >
                {{ node.type === 'LR' ? 'LR' : node.type === 'LS' ? 'LS' : 'EXT' }}
              </text>

              <!-- Node Name -->
              <text
                :x="48"
                :y="30"
                class="node-title"
              >
                {{ node.name }}
              </text>

              <!-- Subtitle / Statistics -->
              <text
                v-if="node.type === 'LR'"
                :x="14"
                :y="56"
                class="node-subtitle"
              >
                Routes: {{ node.properties?.routeCount ?? 0 }} • NAT: {{ node.properties?.natCount ?? 0 }}
              </text>
              <text
                v-else-if="node.type === 'LS'"
                :x="14"
                :y="52"
                class="node-subtitle"
              >
                {{ node.properties?.portCount ?? 0 }} Ports ({{ node.properties?.boundPortCount ?? 0 }} bound)
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
    stroke-dasharray: 6 3;
    opacity: 0.9;
  }

  .vpc-header-bg {
    fill: var(--body-bg);
    stroke: var(--border);
    stroke-width: 1px;
  }

  .vpc-title {
    font-size: 11px;
    font-weight: 700;
    fill: var(--body-text);
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  // Edges
  .edge-line {
    fill: none;
    stroke: var(--border);
    stroke-width: 2px;
  }

  .arrowhead-path {
    fill: var(--border);
  }

  .edge-label-bg {
    fill: var(--box-bg);
    stroke: var(--primary);
    stroke-width: 1.25px;
    filter: drop-shadow(0 2px 4px rgba(0, 0, 0, 0.1));
  }

  .edge-label-text {
    font-size: 11px;
    font-weight: 600;
    fill: var(--primary);
    text-anchor: middle;
    font-family: monospace;
  }

  // Nodes
  .node-group {
    cursor: pointer;

    .node-box {
      fill: var(--box-bg);
      stroke: var(--border);
      stroke-width: 1.5px;
      transition: stroke 0.2s, stroke-width 0.2s;
      filter: drop-shadow(0 2px 6px rgba(0, 0, 0, 0.08));
    }

    .node-accent-bar {
      fill: var(--border);
    }

    &.router-node .node-accent-bar {
      fill: var(--primary);
    }

    &.switch-node .node-accent-bar {
      fill: var(--success);
    }

    &.external-node .node-accent-bar {
      fill: var(--warning);
    }

    .type-badge-rect {
      fill: var(--body-bg);
      stroke: var(--border);
      stroke-width: 1px;
    }

    .type-badge-text {
      font-size: 10px;
      font-weight: 800;
      fill: var(--body-text);
      text-anchor: middle;
    }

    .node-title {
      font-size: 13px;
      font-weight: 600;
      fill: var(--body-text);
    }

    .node-subtitle {
      font-size: 11px;
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
  }
}
</style>
