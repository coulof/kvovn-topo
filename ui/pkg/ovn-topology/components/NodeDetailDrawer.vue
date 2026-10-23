<script lang="ts">
import { defineComponent, ref, PropType, watch } from 'vue';
import { NodeDetail, TopologyApiService } from '../services/api';
import { PositionedNode } from '../utils/layout';

export default defineComponent({
  'name':  'NodeDetailDrawer',
  'props': {
    'node': {
      'type':    Object as PropType<PositionedNode | null>,
      'default': null,
    },
    'apiService': {
      'type':     Object as PropType<TopologyApiService>,
      'required': true,
    },
  },
  'emits': ['close'],
  setup(props) {
    const detail = ref<NodeDetail | null>(null);
    const loading = ref(false);
    const showRaw = ref(false);

    const fetchDetail = async(id: string) => {
      loading.value = true;
      try {
        detail.value = await props.apiService.getNode(id);
      } catch (err) {
        detail.value = null;
      } finally {
        loading.value = false;
      }
    };

    watch(
      () => props.node,
      (newNode) => {
        if (newNode) {
          fetchDetail(newNode.id);
        } else {
          detail.value = null;
        }
      },
      { 'immediate': true }
    );

    return {
      detail,
      loading,
      showRaw,
    };
  },
});
</script>

<template>
  <div
    v-if="node"
    class="detail-drawer"
  >
    <div class="drawer-header">
      <div class="header-left">
        <span
          class="badge"
          :class="node.type.toLowerCase()"
        >{{ node.type }}</span>
        <h3>{{ node.name }}</h3>
      </div>
      <button
        class="close-btn"
        @click="$emit('close')"
      >
        &times;
      </button>
    </div>

    <div
      v-if="loading"
      class="drawer-loading"
    >
      <span>Loading node details...</span>
    </div>

    <div
      v-else
      class="drawer-content"
    >
      <div class="section">
        <h4>Overview</h4>
        <div class="kv-grid">
          <span class="key">ID:</span>
          <span class="val mono">{{ node.id }}</span>
          <span
            v-if="node.parent"
            class="key"
          >VPC:</span>
          <span
            v-if="node.parent"
            class="val"
          >{{ node.parent }}</span>
        </div>
      </div>

      <!-- Router Specific Information -->
      <div
        v-if="node.type === 'LR' && node.properties"
        class="section"
      >
        <h4>L3 Routing Statistics</h4>
        <div class="stat-pills">
          <div class="pill">
            <span class="num">{{ node.properties.routeCount ?? 0 }}</span>
            <span class="lbl">Static Routes</span>
          </div>
          <div class="pill">
            <span class="num">{{ node.properties.natCount ?? 0 }}</span>
            <span class="lbl">NAT Rules</span>
          </div>
          <div class="pill">
            <span class="num">{{ node.properties.policyCount ?? 0 }}</span>
            <span class="lbl">Policies</span>
          </div>
          <div class="pill">
            <span class="num">{{ node.properties.lbCount ?? 0 }}</span>
            <span class="lbl">LoadBalancers</span>
          </div>
        </div>
      </div>

      <!-- Gateway & EIP Information -->
      <div
        v-if="node.type === 'LR' && node.properties?.gateways && node.properties.gateways.length > 0"
        class="section"
      >
        <h4>External Gateway Ports & EIPs</h4>
        <ul class="gateway-list">
          <li
            v-for="gw in node.properties.gateways"
            :key="gw.lrp"
            class="gateway-item"
          >
            <span class="gw-badge">EIP GW</span>
            <span class="gw-name mono">{{ gw.lrp }}</span>
            <span
              v-if="gw.activeChassis"
              class="gw-status active"
            >Active on {{ gw.activeChassis }}</span>
          </li>
        </ul>
      </div>

      <!-- Switch Specific Information -->
      <div
        v-if="node.type === 'LS' && node.properties"
        class="section"
      >
        <h4>L2 Port Status</h4>
        <div class="stat-pills">
          <div class="pill">
            <span class="num">{{ node.properties.portCount ?? 0 }}</span>
            <span class="lbl">Total Ports</span>
          </div>
          <div class="pill bound">
            <span class="num">{{ node.properties.boundPortCount ?? 0 }}</span>
            <span class="lbl">Bound Ports</span>
          </div>
          <div class="pill unbound">
            <span class="num">{{ node.properties.unboundPortCount ?? 0 }}</span>
            <span class="lbl">Unbound</span>
          </div>
        </div>
      </div>

      <!-- Related Objects -->
      <div
        v-if="detail && detail.related && detail.related.length > 0"
        class="section"
      >
        <h4>Connected Graph Objects ({{ detail.related.length }})</h4>
        <ul class="related-list">
          <li
            v-for="rel in detail.related"
            :key="rel.id"
            class="related-item"
          >
            <span class="rel-badge">{{ rel.type }}</span>
            <span class="rel-name">{{ rel.name || rel.id }}</span>
            <span class="rel-role">({{ rel.relationship }})</span>
          </li>
        </ul>
      </div>

      <!-- Kubernetes Metadata -->
      <div
        v-if="node.k8s"
        class="section"
      >
        <h4>Kubernetes Metadata</h4>
        <div class="kv-grid">
          <template v-if="node.k8s.namespace">
            <span class="key">Namespace:</span>
            <span class="val">{{ node.k8s.namespace }}</span>
          </template>
          <template v-if="node.k8s.pod">
            <span class="key">Pod:</span>
            <span class="val">{{ node.k8s.pod }}</span>
          </template>
          <template v-if="node.k8s.subnet">
            <span class="key">Subnet:</span>
            <span class="val">{{ node.k8s.subnet }}</span>
          </template>
          <template v-if="node.k8s.node">
            <span class="key">Node:</span>
            <span class="val">{{ node.k8s.node }}</span>
          </template>
          <template v-if="node.k8s.workloadName">
            <span class="key">{{ node.k8s.workloadKind || 'Workload' }}:</span>
            <span class="val">{{ node.k8s.workloadName }}</span>
          </template>
          <template v-if="node.k8s.networkAttachment">
            <span class="key">Attachment:</span>
            <span class="val mono">{{ node.k8s.networkAttachment }}</span>
          </template>
        </div>
      </div>

      <!-- Raw Toggle -->
      <div class="section raw-section">
        <label class="raw-checkbox">
          <input
            v-model="showRaw"
            type="checkbox"
          />
          <span>Show Raw OVSDB Data</span>
        </label>
        <div
          v-if="showRaw && detail"
          class="raw-code"
        >
          <pre><code>{{ JSON.stringify(detail.raw, null, 2) }}</code></pre>
        </div>
      </div>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.detail-drawer {
  position: absolute;
  top: 0;
  right: 0;
  width: 420px;
  height: 100%;
  background-color: var(--box-bg);
  border-left: 1px solid var(--border);
  box-shadow: -4px 0 16px rgba(0, 0, 0, 0.15);
  display: flex;
  flex-direction: column;
  z-index: 10;

  .drawer-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);

    .header-left {
      display: flex;
      align-items: center;
      gap: 10px;

      h3 {
        margin: 0;
        font-size: 16px;
        color: var(--body-text);
      }

      .badge {
        padding: 2px 6px;
        font-size: 10px;
        font-weight: 700;
        border-radius: 4px;
        background-color: var(--primary);
        color: #ffffff;

        &.ls {
          background-color: var(--success);
        }
        &.external {
          background-color: var(--warning);
        }
      }
    }

    .close-btn {
      background: none;
      border: none;
      font-size: 24px;
      line-height: 1;
      color: var(--muted);
      cursor: pointer;

      &:hover {
        color: var(--body-text);
      }
    }
  }

  .drawer-loading {
    padding: 24px;
    color: var(--muted);
  }

  .drawer-content {
    padding: 20px;
    overflow-y: auto;
    flex: 1;

    .section {
      margin-bottom: 24px;

      h4 {
        font-size: 13px;
        font-weight: 600;
        margin-top: 0;
        margin-bottom: 10px;
        color: var(--primary);
        text-transform: uppercase;
        letter-spacing: 0.5px;
      }
    }

    .kv-grid {
      display: grid;
      grid-template-columns: 80px 1fr;
      gap: 8px 12px;
      font-size: 12px;

      .key {
        color: var(--muted);
      }

      .val {
        color: var(--body-text);

        &.mono {
          font-family: monospace;
          word-break: break-all;
        }
      }
    }

    .stat-pills {
      display: grid;
      grid-template-columns: repeat(2, 1fr);
      gap: 10px;

      .pill {
        background-color: var(--body-bg);
        border: 1px solid var(--border);
        border-radius: 6px;
        padding: 8px 12px;
        display: flex;
        flex-direction: column;

        .num {
          font-size: 18px;
          font-weight: 700;
          color: var(--body-text);
        }

        .lbl {
          font-size: 11px;
          color: var(--muted);
        }

        &.bound .num {
          color: var(--success);
        }
        &.unbound .num {
          color: var(--error);
        }
      }
    }

    .gateway-list {
      list-style: none;
      padding: 0;
      margin: 0;

      .gateway-item {
        display: flex;
        align-items: center;
        gap: 8px;
        padding: 6px 0;
        border-bottom: 1px solid var(--subtle-border, var(--border));
        font-size: 12px;

        .gw-badge {
          background-color: #fe7c3f;
          color: #ffffff;
          padding: 1px 6px;
          border-radius: 3px;
          font-size: 10px;
          font-weight: 700;
        }

        .gw-name {
          font-family: monospace;
          color: var(--body-text);
          flex: 1;
        }

        .gw-status.active {
          color: #fe7c3f;
          font-weight: 600;
          font-size: 11px;
        }
      }
    }

    .related-list {
      list-style: none;
      padding: 0;
      margin: 0;

      .related-item {
        display: flex;
        align-items: center;
        gap: 8px;
        padding: 6px 0;
        border-bottom: 1px solid var(--subtle-border, var(--border));
        font-size: 12px;

        .rel-badge {
          background-color: var(--body-bg);
          border: 1px solid var(--border);
          padding: 1px 4px;
          border-radius: 3px;
          font-size: 10px;
          color: var(--muted);
        }

        .rel-name {
          font-family: monospace;
          color: var(--body-text);
          flex: 1;
        }

        .rel-role {
          color: var(--muted);
          font-size: 11px;
        }
      }
    }

    .raw-section {
      border-top: 1px solid var(--border);
      padding-top: 16px;

      .raw-checkbox {
        display: flex;
        align-items: center;
        gap: 8px;
        cursor: pointer;
        font-size: 12px;
        color: var(--body-text);
      }

      .raw-code {
        margin-top: 12px;
        background-color: var(--body-bg);
        border: 1px solid var(--border);
        border-radius: 4px;
        padding: 8px;
        max-height: 250px;
        overflow: auto;

        pre {
          margin: 0;
          font-size: 11px;
          color: var(--body-text);
        }
      }
    }
  }
}
</style>
