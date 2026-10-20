import TopologyPage from '../pages/TopologyPage.vue';

const PRODUCT_NAME = 'ovn-topology';
const PAGE_NAME = 'topology';

export default [
  // Primary Cluster Explorer route: /c/:cluster/ovn-topology/topology
  {
    'name':      `c-cluster-${ PRODUCT_NAME }-${ PAGE_NAME }`,
    'path':      `/c/:cluster/${ PRODUCT_NAME }/${ PAGE_NAME }`,
    'component': TopologyPage,
    'meta':      { 'product': PRODUCT_NAME },
  },
  // Injected Harvester route: /c/:cluster/harvester/ovn-topology
  {
    'name':      'c-cluster-harvester-ovn-topology',
    'path':      '/c/:cluster/harvester/ovn-topology',
    'component': TopologyPage,
    'meta':      { 'product': 'harvester' },
  },
];
