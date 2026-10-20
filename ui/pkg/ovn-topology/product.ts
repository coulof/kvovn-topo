import { IPlugin } from '@shell/core/types';

export function init($extension: IPlugin, store: any) {
  const PRODUCT_NAME = 'ovn-topology';
  const PAGE_NAME = 'topology';
  const { product, virtualType, basicType } = $extension.DSL(store, PRODUCT_NAME);

  product({
    'icon':    'network',
    'inStore': 'cluster',
    'weight':  100,
    'to':      {
      'name':   `c-cluster-${ PRODUCT_NAME }-${ PAGE_NAME }`,
      'params': { 'product': PRODUCT_NAME },
    },
  });

  virtualType({
    'labelKey': 'ovnTopology.nav.topology',
    'name':     PAGE_NAME,
    'route':    {
      'name':   `c-cluster-${ PRODUCT_NAME }-${ PAGE_NAME }`,
      'params': { 'product': PRODUCT_NAME },
    },
  });

  basicType([PAGE_NAME]);
}
