import TopologyPage from '../pages/TopologyPage.vue';

const PRODUCT_NAME = 'ovn-topology';
const PAGE_NAME = 'topology';

export default [
  {
    'name':      `c-cluster-${ PRODUCT_NAME }-${ PAGE_NAME }`,
    'path':      `/c/:cluster/${ PRODUCT_NAME }/${ PAGE_NAME }`,
    'component': TopologyPage,
    'meta':      { 'product': PRODUCT_NAME },
  },
];
