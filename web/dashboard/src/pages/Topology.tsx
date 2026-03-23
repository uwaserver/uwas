import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  type Node,
  type Edge,
  Position,
  MarkerType,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { fetchDomains, type DomainData } from '@/lib/api';

function buildGraph(domains: DomainData[]) {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  // Center: UWAS Server
  nodes.push({
    id: 'server',
    data: { label: 'UWAS Server' },
    position: { x: 400, y: 250 },
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    style: {
      background: '#2563eb',
      color: '#fff',
      border: '2px solid #3b82f6',
      borderRadius: '12px',
      padding: '12px 24px',
      fontWeight: 700,
      fontSize: '14px',
    },
  });

  // Left: Clients / Internet
  nodes.push({
    id: 'internet',
    data: { label: 'Internet / Clients' },
    position: { x: 50, y: 250 },
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    style: {
      background: '#334155',
      color: '#e2e8f0',
      border: '2px solid #475569',
      borderRadius: '12px',
      padding: '10px 20px',
      fontWeight: 600,
      fontSize: '13px',
    },
  });

  edges.push({
    id: 'internet-server',
    source: 'internet',
    target: 'server',
    animated: true,
    style: { stroke: '#2563eb' },
    markerEnd: { type: MarkerType.ArrowClosed, color: '#2563eb' },
    label: 'HTTPS',
    labelStyle: { fill: '#94a3b8', fontSize: 11 },
    labelBgStyle: { fill: '#0f172a' },
  });

  // Right: Domains
  const typeColors: Record<string, string> = {
    static: '#3b82f6',
    php: '#a855f7',
    proxy: '#f97316',
    redirect: '#64748b',
  };

  const startY = Math.max(0, 250 - (domains.length * 90) / 2);

  domains.forEach((d, i) => {
    const domainId = `domain-${i}`;
    const yPos = startY + i * 90;
    const color = typeColors[d.type] ?? '#64748b';

    nodes.push({
      id: domainId,
      data: { label: `${d.host}\n(${d.type})` },
      position: { x: 750, y: yPos },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      style: {
        background: '#1e293b',
        color: '#e2e8f0',
        border: `2px solid ${color}`,
        borderRadius: '8px',
        padding: '8px 16px',
        fontSize: '12px',
        whiteSpace: 'pre-line' as const,
        textAlign: 'center' as const,
      },
    });

    edges.push({
      id: `server-${domainId}`,
      source: 'server',
      target: domainId,
      style: { stroke: color },
      markerEnd: { type: MarkerType.ArrowClosed, color },
    });

    // Proxy backends
    if (d.type === 'proxy' && d.root) {
      const backendId = `backend-${i}`;
      nodes.push({
        id: backendId,
        data: { label: d.root },
        position: { x: 1080, y: yPos },
        sourcePosition: Position.Right,
        targetPosition: Position.Left,
        style: {
          background: '#1e293b',
          color: '#f97316',
          border: '1px dashed #f97316',
          borderRadius: '6px',
          padding: '6px 12px',
          fontSize: '11px',
        },
      });

      edges.push({
        id: `${domainId}-${backendId}`,
        source: domainId,
        target: backendId,
        animated: true,
        style: { stroke: '#f97316', strokeDasharray: '5 5' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#f97316' },
      });
    }
  });

  return { nodes, edges };
}

export default function Topology() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);

  const loadDomains = useCallback(() => {
    fetchDomains()
      .then(d => setDomains(d ?? []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    loadDomains();
  }, [loadDomains]);

  const { nodes, edges } = useMemo(() => buildGraph(domains), [domains]);

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">
        Loading topology...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">Topology</h1>
        <p className="text-sm text-slate-400">
          Visual map of request flow through UWAS
        </p>
      </div>

      <div className="h-[calc(100vh-12rem)] rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          fitView
          proOptions={{ hideAttribution: true }}
          style={{ background: '#1e293b' }}
        >
          <Background color="#334155" gap={20} />
          <Controls
            style={{ background: '#1e293b', border: '1px solid #334155' }}
          />
        </ReactFlow>
      </div>
    </div>
  );
}
