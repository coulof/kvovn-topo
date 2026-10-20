import { components } from '../types/api';

export type GraphResponse = components['schemas']['GraphResponse'];
export type GraphNode = components['schemas']['GraphNode'];
export type GraphEdge = components['schemas']['GraphEdge'];
export type NodeDetail = components['schemas']['NodeDetail'];
export type VersionInfo = components['schemas']['VersionInfo'];
export type ChassisList = components['schemas']['ChassisList'];

export class TopologyApiService {
  private clusterId: string;
  private namespace: string;
  private store: any;

  constructor(store: any, clusterId: string, namespace = 'kube-system') {
    this.store = store;
    this.clusterId = clusterId;
    this.namespace = namespace;
  }

  private get baseUrl(): string {
    return `/k8s/clusters/${ this.clusterId }/api/v1/namespaces/${ this.namespace }/services/http:ovn-topologyd:8080/proxy/api/v1`;
  }

  async getVersion(): Promise<VersionInfo> {
    const res = await this.store.dispatch('cluster/request', {
      'url':                  `${ this.baseUrl }/version`,
      'redirectUnauthorized': false,
    });

    return res;
  }

  async getGraph(view = 'l3', focus?: string, depth = 1): Promise<GraphResponse> {
    const params = new URLSearchParams();

    if (view) {
      params.set('view', view);
    }
    if (focus) {
      params.set('focus', focus);
    }
    if (depth) {
      params.set('depth', depth.toString());
    }

    const url = `${ this.baseUrl }/graph?${ params.toString() }`;
    const res = await this.store.dispatch('cluster/request', {
      url,
      'redirectUnauthorized': false,
    });

    return res;
  }

  async getNode(id: string): Promise<NodeDetail> {
    const res = await this.store.dispatch('cluster/request', {
      'url':                  `${ this.baseUrl }/node/${ encodeURIComponent(id) }`,
      'redirectUnauthorized': false,
    });

    return res;
  }

  async getChassis(): Promise<ChassisList> {
    const res = await this.store.dispatch('cluster/request', {
      'url':                  `${ this.baseUrl }/chassis`,
      'redirectUnauthorized': false,
    });

    return res;
  }
}
