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

export function clearLayoutCache(clusterId: string, view = 'l3') {
  try {
    localStorage.removeItem(`ovn_topology_pos_v2_${ clusterId }_${ view }`);
  } catch (e) {
    // Silently ignore localStorage failures
  }
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
          'elk.padding':                               '[top=65,left=40,bottom=65,right=40]',
          'elk.algorithm':                             'layered',
          'elk.direction':                             'DOWN',
          'elk.spacing.nodeNode':                      '80',
          'elk.layered.spacing.nodeNodeBetweenLayers': '140',
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

    const w = n.type === 'LR' ? 220 : n.type === 'External' ? 180 : 200;
    const h = n.type === 'LR' ? 75 : n.type === 'External' ? 55 : 65;

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
      'elk.spacing.nodeNode':                      '100',
      'elk.layered.spacing.nodeNodeBetweenLayers': '160',
      'elk.spacing.componentComponent':            '90',
      'elk.padding':                               '[top=60,left=60,bottom=60,right=60]',
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
        'y': absY
      });

      const original = nodeMap.get(child.id);

      if (original) {
        const defW = original.type === 'LR' ? 220 : original.type === 'External' ? 180 : 200;
        const defH = original.type === 'LR' ? 75 : original.type === 'External' ? 55 : 65;

        positionedNodes.push({
          ...original,
          'x':          absX,
          'y':          absY,
          'width':      child.children && child.children.length > 0 ? (child.width || 250) : defW,
          'height':     child.children && child.children.length > 0 ? (child.height || 180) : defH,
          'isCompound': child.children && child.children.length > 0,
        });
      }

      if (child.children && child.children.length > 0) {
        collectNodes(child, absX, absY);
      }
    }
  }

  collectNodes(layoutResult, 0, 0);

  // Index edges per node to distribute attachment ports cleanly
  const outgoingMap = new Map<string, GraphEdge[]>();
  const incomingMap = new Map<string, GraphEdge[]>();

  for (const e of graph.edges) {
    if (nodeMap.has(e.source) && nodeMap.has(e.target)) {
      if (!outgoingMap.has(e.source)) {
        outgoingMap.set(e.source, []);
      }
      outgoingMap.get(e.source)!.push(e);

      if (!incomingMap.has(e.target)) {
        incomingMap.set(e.target, []);
      }
      incomingMap.get(e.target)!.push(e);
    }
  }

  // Position edges with distributed connection points and collision-free label coordinates
  const positionedEdges: PositionedEdge[] = [];

  for (const e of graph.edges) {
    const srcCoords = coordsMap.get(e.source);
    const tgtCoords = coordsMap.get(e.target);
    const srcNode = nodeMap.get(e.source);
    const tgtNode = nodeMap.get(e.target);

    if (srcCoords && tgtCoords && srcNode && tgtNode) {
      const srcW = srcNode.type === 'LR' ? 220 : srcNode.type === 'External' ? 180 : 200;
      const srcH = srcNode.type === 'LR' ? 75 : srcNode.type === 'External' ? 55 : 65;
      const tgtW = tgtNode.type === 'LR' ? 220 : tgtNode.type === 'External' ? 180 : 200;

      const outList = outgoingMap.get(e.source) || [e];
      const inList = incomingMap.get(e.target) || [e];

      const srcIdx = outList.findIndex((item) => item.id === e.id);
      const tgtIdx = inList.findIndex((item) => item.id === e.id);

      const srcRatio = (srcIdx >= 0 && outList.length > 1) ? (srcIdx + 1) / (outList.length + 1) : 0.5;
      const tgtRatio = (tgtIdx >= 0 && inList.length > 1) ? (tgtIdx + 1) / (inList.length + 1) : 0.5;

      const startX = srcCoords.x + srcW * srcRatio;
      const startY = srcCoords.y + srcH;
      const endX = tgtCoords.x + tgtW * tgtRatio;
      const endY = tgtCoords.y;

      const deltaY = endY - startY;
      const midY = startY + deltaY * 0.5;
      const arrowStopY = endY - 4;
      const pathD = `M ${ startX } ${ startY } C ${ startX } ${ midY }, ${ endX } ${ midY }, ${ endX } ${ arrowStopY }`;

      positionedEdges.push({
        ...e,
        pathD,
        'labelX': (startX + endX) / 2,
        'labelY': midY,
      });
    }
  }

  const bounds = {
    'width':  (layoutResult.width || 1200) + 140,
    'height': (layoutResult.height || 800) + 140,
  };

  // Attempt to restore user-persisted node drags from localStorage (v2 cache)
  try {
    const cacheKey = `ovn_topology_pos_v2_${ clusterId }_${ graph.view }`;
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
