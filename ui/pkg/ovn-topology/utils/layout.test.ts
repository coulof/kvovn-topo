/* eslint-env jest */
import { computeLayout } from './layout';
import { GraphResponse } from '../services/api';

describe('computeLayout', () => {
  it('correctly calculates compound VPC container and child node positions', async() => {
    const mockGraph: GraphResponse = {
      'revision': 1,
      'view':     'l3',
      'nodes':    [
        {
          'id':   'vpc:default',
          'type': 'VPC',
          'name': 'default',
        },
        {
          'id':     'lr:ovn-cluster',
          'type':   'LR',
          'name':   'ovn_cluster',
          'parent': 'vpc:default',
        },
        {
          'id':     'ls:ovn-default',
          'type':   'LS',
          'name':   'ovn-default',
          'parent': 'vpc:default',
        },
      ],
      'edges': [
        {
          'id':     'edge:lr-ls',
          'source': 'lr:ovn-cluster',
          'target': 'ls:ovn-default',
          'type':   'router-peer',
          'label':  '10.244.0.1/24',
        },
      ],
    };

    const layout = await computeLayout(mockGraph, 'test-cluster');

    expect(layout.nodes.length).toBe(3);
    expect(layout.edges.length).toBe(1);

    // Verify VPC compound container
    const vpc = layout.nodes.find((n) => n.id === 'vpc:default');

    expect(vpc).toBeDefined();
    expect(vpc?.isCompound).toBe(true);
    expect(vpc!.width).toBeGreaterThan(0);
    expect(vpc!.height).toBeGreaterThan(0);

    // Verify children have coordinates
    const lr = layout.nodes.find((n) => n.id === 'lr:ovn-cluster');
    const ls = layout.nodes.find((n) => n.id === 'ls:ovn-default');

    expect(lr).toBeDefined();
    expect(ls).toBeDefined();
    expect(lr!.x).toBeGreaterThanOrEqual(0);
    expect(ls!.x).toBeGreaterThanOrEqual(0);

    // Verify edge path and label
    const edge = layout.edges[0];

    expect(edge.pathD).toContain('M ');
    expect(edge.pathD).toContain('C ');
    expect(edge.labelX).toBeGreaterThan(0);
    expect(edge.labelY).toBeGreaterThan(0);
  });
});
