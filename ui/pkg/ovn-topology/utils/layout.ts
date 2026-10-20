import ELK, { ElkNode, ElkExtendedEdge } from 'elkjs/lib/elk.bundled.js';
import { GraphResponse, GraphNode, GraphEdge } from '../services/api';

const elk = new ELK();

export interface PositionedNode extends GraphNode {
  x: number;
  y: number;
  width: number;
  height: number;
  isCompound?: boolean;
}

export interface PositionedEdge extends GraphEdge {
  pathD: string;
  labelX?: number;
  labelY?: number;
}

export interface PositionedGraph {
  nodes: PositionedNode[];
  edges: PositionedEdge[];
  bounds: { width: number; height: number };
}

export async function computeLayout(graph: GraphResponse, clusterId: string): Promise<PositionedGraph> {
  const nodeMap = new Map<string, GraphNode>();

  for (const n of graph.nodes) {
    nodeMap.set(n.id, n);
  }

  // Identify VPC compound containers and children
  const vpcNodes = new Map<string, ElkNode>();
  const rootChildren: ElkNode[] = [];

  for (const n of graph.nodes) {
    if (n.type === 'VPC') {
      const vpcContainer: ElkNode = {
        'id':            n.id,
        'layoutOptions': {
          'elk.padding':          '[top=45,left=25,bottom=25,right=25]',
          'elk.algorithm':        'layered',
          'elk.direction':        'DOWN',
          'elk.spacing.nodeNode': '30',
        },
        'children': [],
      };

      vpcNodes.set(n.id, vpcContainer);
      rootChildren.push(vpcContainer);
    }
  }

  for (const n of graph.nodes) {
    if (n.type === 'VPC') {
      continue;
    }

    const w = n.type === 'LR' ? 180 : 160;
    const h = n.type === 'LR' ? 65 : 55;

    const elkChild: ElkNode = {
      'id':     n.id,
      'width':  w,
      'height': h,
    };

    if (n.parent && vpcNodes.has(n.parent)) {
      vpcNodes.get(n.parent)!.children!.push(elkChild);
    } else {
      rootChildren.push(elkChild);
    }
  }

  // Format edges for ELK
  const elkEdges: ElkExtendedEdge[] = [];

  for (const e of graph.edges) {
    if (nodeMap.has(e.source) && nodeMap.has(e.target)) {
      elkEdges.push({
        'id':      e.id,
        'sources': [e.source],
        'targets': [e.target],
      });
    }
  }

  const rootElkNode: ElkNode = {
    'id':            'root',
    'layoutOptions': {
      'elk.algorithm':                             'layered',
      'elk.direction':                             'DOWN',
      'elk.hierarchyHandling':                     'INCLUDE_CHILDREN',
      'elk.spacing.nodeNode':                      '45',
      'elk.layered.spacing.nodeNodeBetweenLayers': '60',
      'elk.padding':                               '[top=30,left=30,bottom=30,right=30]',
    },
    'children': rootChildren,
    'edges':    elkEdges,
  };

  const layoutResult = await elk.layout(rootElkNode);

  // Flatten positioned nodes
  const positionedNodes: PositionedNode[] = [];
  const coordsMap = new Map<string, { x: number; y: number }>();

  function collectNodes(parentElk: ElkNode, offsetX = 0, offsetY = 0) {
    if (!parentElk.children) {
      return;
    }

    for (const child of parentElk.children) {
      const absX = offsetX + (child.x || 0);
      const absY = offsetY + (child.y || 0);

      coordsMap.set(child.id, {
        'x': absX,
        'y':   absY
      });

      const original = nodeMap.get(child.id);

      if (original) {
        positionedNodes.push({
          ...original,
          'x':          absX,
          'y':          absY,
          'width':      child.width || 160,
          'height':     child.height || 60,
          'isCompound': child.children && child.children.length > 0,
        });
      }

      if (child.children && child.children.length > 0) {
        collectNodes(child, absX, absY);
      }
    }
  }

  collectNodes(layoutResult, 0, 0);

  // Position edges
  const positionedEdges: PositionedEdge[] = [];

  for (const e of graph.edges) {
    const srcCoords = coordsMap.get(e.source);
    const tgtCoords = coordsMap.get(e.target);
    const srcNode = nodeMap.get(e.source);
    const tgtNode = nodeMap.get(e.target);

    if (srcCoords && tgtCoords && srcNode && tgtNode) {
      const srcW = srcNode.type === 'LR' ? 180 : 160;
      const srcH = srcNode.type === 'LR' ? 65 : 55;
      const tgtW = tgtNode.type === 'LR' ? 180 : 160;

      const startX = srcCoords.x + srcW / 2;
      const startY = srcCoords.y + srcH;
      const endX = tgtCoords.x + tgtW / 2;
      const endY = tgtCoords.y;

      const midY = (startY + endY) / 2;
      const pathD = `M ${ startX } ${ startY } C ${ startX } ${ midY }, ${ endX } ${ midY }, ${ endX } ${ endY }`;

      positionedEdges.push({
        ...e,
        pathD,
        'labelX': (startX + endX) / 2,
        'labelY': midY,
      });
    }
  }

  const bounds = {
    'width':  (layoutResult.width || 1200) + 100,
    'height': (layoutResult.height || 800) + 100,
  };

  // Attempt to restore user-persisted node drags from localStorage
  try {
    const cacheKey = `ovn_topology_pos_${ clusterId }_${ graph.view }`;
    const saved = localStorage.getItem(cacheKey);

    if (saved) {
      const customPositions = JSON.parse(saved);

      for (const pNode of positionedNodes) {
        if (customPositions[pNode.id]) {
          pNode.x = customPositions[pNode.id].x;
          pNode.y = customPositions[pNode.id].y;
        }
      }
    }
  } catch (err) {
    // LocalStorage failure in restricted environments should degrade silently
  }

  return {
    'nodes': positionedNodes,
    'edges': positionedEdges,
    bounds,
  };
}
