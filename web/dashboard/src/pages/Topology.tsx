import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  type Node,
  type Edge,
  type OnNodesChange,
  Position,
  MarkerType,
  applyNodeChanges,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import {
  fetchDomains, fetchCerts, fetchDomainPHPInstances,
  type DomainData, type CertInfo, type DomainPHP,
} from '@/lib/api';

/* ------------------------------------------------------------------ */
/*  Graph builder                                                      */
/* ------------------------------------------------------------------ */

interface TopologyData {
  domains: DomainData[];
  certs: Record<string, CertInfo>;
  phpMap: Record<string, DomainPHP>; // domain → php assignment
}

function buildGraph({ domains, certs, phpMap }: TopologyData) {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  const typeColors: Record<string, string> = {
    static: '#3b82f6',
    php: '#a855f7',
    proxy: '#f97316',
    app: '#22c55e',
    redirect: '#64748b',
  };

  // ---- Internet node (left) ----
  nodes.push({
    id: 'internet',
    data: { label: 'Internet' },
    position: { x: 0, y: 250 },
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    style: {
      background: '#334155', color: '#e2e8f0', border: '2px solid #475569',
      borderRadius: '50%', width: 90, height: 90, display: 'flex',
      alignItems: 'center', justifyContent: 'center', fontWeight: 700, fontSize: '12px',
    },
  });

  // ---- UWAS Server node (center) ----
  nodes.push({
    id: 'server',
    data: { label: 'UWAS' },
    position: { x: 220, y: 230 },
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    style: {
      background: '#2563eb', color: '#fff', border: '2px solid #3b82f6',
      borderRadius: '12px', padding: '14px 28px', fontWeight: 700, fontSize: '16px',
    },
  });

  edges.push({
    id: 'internet-server',
    source: 'internet',
    target: 'server',
    animated: true,
    style: { stroke: '#2563eb', strokeWidth: 2 },
    markerEnd: { type: MarkerType.ArrowClosed, color: '#2563eb' },
    label: 'HTTP/S',
    labelStyle: { fill: '#94a3b8', fontSize: 10 },
    labelBgStyle: { fill: '#1e293b', fillOpacity: 0.8 },
  });

  // ---- PHP version nodes (shared, right side) ----
  const phpVersions = new Set<string>();
  for (const [, php] of Object.entries(phpMap)) {
    phpVersions.add(php.version);
  }
  const phpVersionArr = [...phpVersions];
  const phpStartY = Math.max(0, 250 - (phpVersionArr.length * 100) / 2);

  phpVersionArr.forEach((ver, i) => {
    nodes.push({
      id: `php-${ver}`,
      data: { label: `PHP ${ver}` },
      position: { x: 850, y: phpStartY + i * 100 },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      style: {
        background: '#1e293b', color: '#c084fc', border: '2px solid #a855f7',
        borderRadius: '10px', padding: '10px 20px', fontWeight: 700, fontSize: '13px',
      },
    });
  });

  // ---- Domain nodes ----
  const startY = Math.max(0, 250 - (domains.length * 80) / 2);

  domains.forEach((d, i) => {
    const domainId = `domain-${i}`;
    const yPos = startY + i * 80;
    const color = typeColors[d.type] ?? '#64748b';
    const cert = certs[d.host];
    const php = phpMap[d.host];

    // Build badge line
    const badges: string[] = [];
    if (cert?.status === 'active') badges.push('🔒');
    else if (d.ssl === 'auto') badges.push('⏳');
    else if (d.ssl === 'off') badges.push('🔓');
    // We don't have WAF/cache from DomainData (list endpoint), so show type
    badges.push(d.type.toUpperCase());

    const label = `${d.host}\n${badges.join(' · ')}`;

    nodes.push({
      id: domainId,
      data: { label },
      position: { x: 500, y: yPos },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      style: {
        background: '#0f172a', color: '#e2e8f0', border: `2px solid ${color}`,
        borderRadius: '8px', padding: '8px 14px', fontSize: '11px',
        whiteSpace: 'pre-line' as const, textAlign: 'center' as const,
        lineHeight: '1.4',
      },
    });

    // Server → Domain edge
    edges.push({
      id: `server-${domainId}`,
      source: 'server',
      target: domainId,
      style: { stroke: color },
      markerEnd: { type: MarkerType.ArrowClosed, color },
    });

    // Domain → PHP edge
    if (php) {
      edges.push({
        id: `${domainId}-php-${php.version}`,
        source: domainId,
        target: `php-${php.version}`,
        animated: php.running,
        style: { stroke: '#a855f7', strokeDasharray: php.running ? undefined : '5 5' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#a855f7' },
        label: php.running ? `${php.listen_addr}` : 'stopped',
        labelStyle: { fill: '#94a3b8', fontSize: 9 },
        labelBgStyle: { fill: '#1e293b', fillOpacity: 0.8 },
      });
    }

    // Domain → Proxy backend
    if (d.type === 'proxy' && d.root) {
      const backendId = `backend-${i}`;
      nodes.push({
        id: backendId,
        data: { label: d.root },
        position: { x: 850, y: yPos },
        sourcePosition: Position.Right,
        targetPosition: Position.Left,
        style: {
          background: '#1e293b', color: '#f97316', border: '1px dashed #f97316',
          borderRadius: '6px', padding: '6px 12px', fontSize: '10px',
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

    // Domain → App process
    if (d.type === 'app') {
      const appId = `app-${i}`;
      nodes.push({
        id: appId,
        data: { label: `App Process\n127.0.0.1:${d.root || '3000'}` },
        position: { x: 850, y: yPos },
        sourcePosition: Position.Right,
        targetPosition: Position.Left,
        style: {
          background: '#1e293b', color: '#22c55e', border: '2px solid #22c55e',
          borderRadius: '10px', padding: '8px 16px', fontSize: '10px',
          whiteSpace: 'pre-line' as const, textAlign: 'center' as const,
        },
      });
      edges.push({
        id: `${domainId}-${appId}`,
        source: domainId,
        target: appId,
        animated: true,
        style: { stroke: '#22c55e' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#22c55e' },
        label: 'reverse proxy',
        labelStyle: { fill: '#94a3b8', fontSize: 9 },
        labelBgStyle: { fill: '#1e293b', fillOpacity: 0.8 },
      });
    }
  });

  return { nodes, edges };
}

/* ------------------------------------------------------------------ */
/*  Component                                                          */
/* ------------------------------------------------------------------ */

export default function Topology() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [certs, setCerts] = useState<Record<string, CertInfo>>({});
  const [phpMap, setPhpMap] = useState<Record<string, DomainPHP>>({});
  const [loading, setLoading] = useState(true);

  const loadAll = useCallback(() => {
    Promise.all([fetchDomains(), fetchCerts(), fetchDomainPHPInstances()])
      .then(([d, c, p]) => {
        setDomains(d ?? []);
        const certMap: Record<string, CertInfo> = {};
        for (const cert of (c ?? [])) certMap[cert.host] = cert;
        setCerts(certMap);
        const pm: Record<string, DomainPHP> = {};
        for (const inst of (p ?? [])) pm[inst.domain] = inst;
        setPhpMap(pm);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { loadAll(); }, [loadAll]);

  const initial = useMemo(
    () => buildGraph({ domains, certs, phpMap }),
    [domains, certs, phpMap],
  );

  // Draggable nodes — keep local state so drag doesn't reset
  const [nodes, setNodes] = useState<Node[]>([]);
  useEffect(() => { setNodes(initial.nodes); }, [initial.nodes]);

  const onNodesChange: OnNodesChange = useCallback(
    (changes) => setNodes((nds) => applyNodeChanges(changes, nds)),
    [],
  );

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-muted-foreground">
        Loading topology...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold sm:text-2xl text-foreground">Topology</h1>
        <p className="text-sm text-muted-foreground">
          Drag nodes to rearrange. Connections follow automatically.
        </p>
      </div>

      <div className="h-[calc(100vh-12rem)] rounded-lg border border-border bg-card shadow-md">
        <ReactFlow
          nodes={nodes}
          edges={initial.edges}
          onNodesChange={onNodesChange}
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

      {/* Legend */}
      <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-full bg-[#3b82f6]" /> Static</span>
        <span className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-full bg-[#a855f7]" /> PHP</span>
        <span className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-full bg-[#f97316]" /> Proxy</span>
        <span className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-full bg-[#64748b]" /> Redirect</span>
        <span>|</span>
        <span>🔒 SSL Active</span>
        <span>⏳ SSL Pending</span>
        <span>🔓 No SSL</span>
      </div>
    </div>
  );
}
