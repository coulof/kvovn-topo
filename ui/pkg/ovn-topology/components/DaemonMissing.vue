<script lang="ts">
import { defineComponent } from 'vue';
import Banner from '@shell/rancher-components/Banner/Banner.vue';

export default defineComponent({
  'name':       'DaemonMissing',
  'components': { Banner },
  'props':      {
    'clusterId': {
      'type':     String,
      'required': true,
    },
  },
  setup() {
    const helmCommand = 'helm install ovn-topology oci://ghcr.io/coulof/charts/ovn-topology --namespace kube-system';

    return { helmCommand };
  },
});
</script>

<template>
  <div class="daemon-missing-container">
    <Banner
      color="warning"
      class="banner"
    >
      <div class="content">
        <h3>{{ t('ovnTopology.daemonMissing.title') }}</h3>
        <p>{{ t('ovnTopology.daemonMissing.description') }}</p>
        <div class="command-box">
          <code>{{ helmCommand }}</code>
        </div>
      </div>
    </Banner>
  </div>
</template>

<style lang="scss" scoped>
.daemon-missing-container {
  padding: 24px;
  max-width: 800px;
  margin: 40px auto;

  .content {
    h3 {
      margin-top: 0;
      margin-bottom: 8px;
    }

    p {
      margin-bottom: 16px;
      color: var(--body-text);
    }

    .command-box {
      background-color: var(--box-bg);
      border: 1px solid var(--border);
      border-radius: 4px;
      padding: 12px 16px;

      code {
        font-family: monospace;
        color: var(--primary);
        user-select: all;
      }
    }
  }
}
</style>
