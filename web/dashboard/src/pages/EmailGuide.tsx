import { useState, useEffect } from 'react';
import { Mail, Copy, Check, ExternalLink } from 'lucide-react';
import { fetchDomains, fetchServerIPs, type DomainData } from '@/lib/api';

export default function EmailGuide() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [serverIP, setServerIP] = useState('');
  const [copied, setCopied] = useState('');

  useEffect(() => {
    fetchDomains().then(d => { const list = d ?? []; setDomains(list); if (list.length > 0) setSelectedDomain(list[0].host); }).catch(() => {});
    fetchServerIPs().then(data => setServerIP(data?.public_ip || '')).catch(() => {});
  }, []);

  const copy = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopied(label);
    setTimeout(() => setCopied(''), 2000);
  };

  const domain = selectedDomain;
  const baseDomain = domain.split('.').slice(-2).join('.');

  const records = [
    {
      type: 'MX',
      name: baseDomain,
      value: `10 mail.${baseDomain}`,
      description: 'Points email to your mail server',
      priority: 'Required',
    },
    {
      type: 'A',
      name: `mail.${baseDomain}`,
      value: serverIP || 'YOUR_SERVER_IP',
      description: 'Mail server hostname resolves to your server',
      priority: 'Required',
    },
    {
      type: 'TXT (SPF)',
      name: baseDomain,
      value: `v=spf1 ip4:${serverIP || 'YOUR_SERVER_IP'} -all`,
      description: 'Authorizes your server to send email for this domain',
      priority: 'Required',
    },
    {
      type: 'TXT (DMARC)',
      name: `_dmarc.${baseDomain}`,
      value: `v=DMARC1; p=quarantine; rua=mailto:admin@${baseDomain}`,
      description: 'Tells receivers what to do with unauthenticated email',
      priority: 'Recommended',
    },
    {
      type: 'TXT (DKIM)',
      name: `default._domainkey.${baseDomain}`,
      value: 'v=DKIM1; k=rsa; p=YOUR_PUBLIC_KEY',
      description: 'Cryptographic signature for outgoing emails (generate key with your mail server)',
      priority: 'Recommended',
    },
    {
      type: 'PTR (rDNS)',
      name: serverIP || 'YOUR_SERVER_IP',
      value: `mail.${baseDomain}`,
      description: 'Reverse DNS — set via your hosting provider\'s control panel',
      priority: 'Required',
    },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold sm:text-2xl text-foreground">Email Configuration</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          DNS records needed for email delivery. Add these at your DNS provider.
        </p>
      </div>

      {/* Domain selector */}
      <div className="flex items-center gap-3">
        <label className="text-sm text-muted-foreground">Domain:</label>
        <select
          value={selectedDomain}
          onChange={e => setSelectedDomain(e.target.value)}
          className="rounded-md border border-border bg-card px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500"
        >
          {domains.map(d => <option key={d.host} value={d.host}>{d.host}</option>)}
        </select>
      </div>

      {/* Info banner */}
      <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 p-4 text-sm">
        <div className="flex items-center gap-2 text-blue-400 font-medium mb-1">
          <Mail size={16} /> UWAS does not include a mail server
        </div>
        <p className="text-muted-foreground text-xs">
          For email, install <strong>Postfix + Dovecot</strong> or use an external service
          (Gmail SMTP, SendGrid, Mailgun). The DNS records below are needed regardless of your mail setup.
        </p>
      </div>

      {/* DNS Records table */}
      <div className="overflow-hidden rounded-lg border border-border">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-card/50 text-left text-xs uppercase tracking-wider text-muted-foreground">
              <th className="px-4 py-3">Type</th>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Value</th>
              <th className="px-4 py-3">Priority</th>
              <th className="px-4 py-3 w-10"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {records.map((r, i) => (
              <tr key={i} className="bg-background hover:bg-card/50">
                <td className="px-4 py-3">
                  <span className="rounded bg-purple-500/15 px-2 py-0.5 text-xs font-medium text-purple-400">{r.type}</span>
                </td>
                <td className="px-4 py-3 font-mono text-xs text-card-foreground">{r.name}</td>
                <td className="px-4 py-3">
                  <code className="rounded bg-card px-2 py-1 text-xs font-mono text-foreground select-all">{r.value}</code>
                  <p className="text-[10px] text-muted-foreground mt-1">{r.description}</p>
                </td>
                <td className="px-4 py-3">
                  <span className={`text-xs font-medium ${r.priority === 'Required' ? 'text-red-400' : 'text-amber-400'}`}>
                    {r.priority}
                  </span>
                </td>
                <td className="px-4 py-3">
                  <button onClick={() => copy(r.value, `r-${i}`)} className="rounded p-1 text-muted-foreground hover:text-card-foreground">
                    {copied === `r-${i}` ? <Check size={13} className="text-emerald-400" /> : <Copy size={13} />}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Mail server options */}
      <div className="rounded-lg border border-border bg-card p-5">
        <h2 className="text-sm font-semibold text-card-foreground mb-3">Mail Server Options</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {[
            { name: 'Postfix + Dovecot', desc: 'Self-hosted, full control', cmd: 'apt install postfix dovecot-imapd' },
            { name: 'Mail-in-a-Box', desc: 'All-in-one mail server', cmd: 'curl -s https://mailinabox.email/setup.sh | sudo bash' },
            { name: 'External SMTP', desc: 'Gmail, SendGrid, Mailgun', cmd: 'No server install needed — configure app to use SMTP relay' },
          ].map(opt => (
            <div key={opt.name} className="rounded-md bg-background p-3">
              <p className="text-xs font-medium text-card-foreground">{opt.name}</p>
              <p className="text-[10px] text-muted-foreground mb-2">{opt.desc}</p>
              <code className="block rounded bg-card px-2 py-1 text-[10px] font-mono text-muted-foreground select-all">{opt.cmd}</code>
            </div>
          ))}
        </div>
      </div>

      {/* Testing link */}
      <div className="text-xs text-muted-foreground flex items-center gap-1">
        Test your email config:
        <a href="https://mxtoolbox.com" target="_blank" rel="noopener" className="text-blue-400 hover:underline flex items-center gap-0.5">
          MXToolbox <ExternalLink size={10} />
        </a>
        <span className="mx-1">|</span>
        <a href="https://mail-tester.com" target="_blank" rel="noopener" className="text-blue-400 hover:underline flex items-center gap-0.5">
          Mail-tester <ExternalLink size={10} />
        </a>
      </div>
    </div>
  );
}
