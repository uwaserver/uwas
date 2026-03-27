import { useState, useRef, useEffect, useCallback } from 'react';
import { Terminal as TerminalIcon, X, AlertTriangle } from 'lucide-react';
import { terminalWSURL } from '@/lib/api';

export default function TerminalPage() {
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState('');
  const [output, setOutput] = useState('');
  const wsRef = useRef<WebSocket | null>(null);
  const outputRef = useRef<HTMLPreElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const connect = useCallback(() => {
    setError('');
    setOutput('');
    const url = terminalWSURL();
    const ws = new WebSocket(url);
    wsRef.current = ws;

    ws.onopen = () => {
      setConnected(true);
      // Send initial resize
      ws.send(JSON.stringify({ type: 'resize', cols: 120, rows: 40 }));
    };

    ws.onmessage = (e) => {
      setOutput(prev => {
        const next = prev + e.data;
        // Keep last 100KB to prevent memory issues
        return next.length > 100_000 ? next.slice(-80_000) : next;
      });
    };

    ws.onerror = async () => {
      setError(`WebSocket failed. URL: ${url.slice(0, 100)}...`);
    };
    ws.onclose = (e) => {
      setConnected(false);
      wsRef.current = null;
      if (e.code === 1006) setError('Connection lost (abnormal close). Server may have rejected the WebSocket upgrade.');
      else if (e.code !== 1000 && e.code !== 1005) setError(`Connection closed: code ${e.code} ${e.reason || ''}`);
    };
  }, []);

  const disconnect = () => {
    wsRef.current?.close();
    setConnected(false);
  };

  // Auto-scroll to bottom
  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [output]);

  // Cleanup on unmount
  useEffect(() => {
    return () => { wsRef.current?.close(); };
  }, []);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      const val = (e.target as HTMLTextAreaElement).value;
      wsRef.current?.send(val + '\n');
      (e.target as HTMLTextAreaElement).value = '';
    } else if (e.ctrlKey && e.key === 'c') {
      wsRef.current?.send('\x03'); // Ctrl+C
    } else if (e.ctrlKey && e.key === 'd') {
      wsRef.current?.send('\x04'); // Ctrl+D
    } else if (e.ctrlKey && e.key === 'l') {
      e.preventDefault();
      setOutput('');
      wsRef.current?.send('\x0c'); // Ctrl+L (clear)
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Terminal</h1>
          <p className="text-sm text-muted-foreground">Browser-based shell access (Linux only)</p>
        </div>
        {!connected ? (
          <button onClick={connect}
            className="flex items-center gap-1.5 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700">
            <TerminalIcon size={14} /> Connect
          </button>
        ) : (
          <button onClick={disconnect}
            className="flex items-center gap-1.5 rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700">
            <X size={14} /> Disconnect
          </button>
        )}
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
          <AlertTriangle size={14} /> {error}
        </div>
      )}

      <div className="rounded-lg border border-border bg-[#0d1117] overflow-hidden">
        {/* Terminal output */}
        <pre ref={outputRef}
          className="h-[60vh] overflow-auto p-4 font-mono text-xs text-green-400 leading-5 whitespace-pre-wrap"
          onClick={() => inputRef.current?.focus()}>
          {output || (connected ? 'Connected. Waiting for shell...\n' : 'Click "Connect" to start a terminal session.\n')}
        </pre>

        {/* Input area */}
        {connected && (
          <div className="border-t border-border/50 bg-[#161b22] p-2">
            <textarea
              ref={inputRef}
              onKeyDown={handleKeyDown}
              rows={1}
              autoFocus
              placeholder="Type command and press Enter..."
              className="w-full resize-none bg-transparent px-2 py-1 font-mono text-xs text-green-300 outline-none placeholder:text-green-800"
            />
          </div>
        )}
      </div>

      <p className="text-[10px] text-muted-foreground">
        Shortcuts: <kbd className="rounded bg-accent px-1">Ctrl+C</kbd> interrupt &middot;
        <kbd className="rounded bg-accent px-1 ml-1">Ctrl+D</kbd> EOF &middot;
        <kbd className="rounded bg-accent px-1 ml-1">Ctrl+L</kbd> clear
      </p>
    </div>
  );
}
