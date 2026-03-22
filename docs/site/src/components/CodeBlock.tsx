import { useState } from 'react'
import { Copy, Check } from 'lucide-react'

interface CodeBlockProps {
  code: string
  language?: string
  title?: string
  showLineNumbers?: boolean
}

export default function CodeBlock({ code, language = 'bash', title, showLineNumbers = false }: CodeBlockProps) {
  const [copied, setCopied] = useState(false)

  function handleCopy() {
    navigator.clipboard.writeText(code).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  const lines = code.split('\n')

  return (
    <div className="group overflow-hidden rounded-xl border border-uwas-border bg-uwas-code-bg">
      {title && (
        <div className="flex items-center justify-between border-b border-uwas-border px-4 py-2.5">
          <div className="flex items-center gap-2">
            <div className="flex gap-1.5">
              <div className="h-3 w-3 rounded-full bg-red-500/70" />
              <div className="h-3 w-3 rounded-full bg-yellow-500/70" />
              <div className="h-3 w-3 rounded-full bg-green-500/70" />
            </div>
            <span className="ml-2 text-xs font-medium text-uwas-text-muted">{title}</span>
          </div>
          <button
            onClick={handleCopy}
            className="rounded-md p-1.5 text-uwas-text-muted opacity-0 transition-all hover:bg-uwas-bg-light hover:text-uwas-text group-hover:opacity-100"
            title="Copy to clipboard"
          >
            {copied ? <Check className="h-4 w-4 text-uwas-green" /> : <Copy className="h-4 w-4" />}
          </button>
        </div>
      )}
      <div className="relative">
        {!title && (
          <button
            onClick={handleCopy}
            className="absolute right-3 top-3 rounded-md p-1.5 text-uwas-text-muted opacity-0 transition-all hover:bg-uwas-bg-light hover:text-uwas-text group-hover:opacity-100"
            title="Copy to clipboard"
          >
            {copied ? <Check className="h-4 w-4 text-uwas-green" /> : <Copy className="h-4 w-4" />}
          </button>
        )}
        <pre className="overflow-x-auto p-4">
          <code className={`language-${language}`}>
            {showLineNumbers
              ? lines.map((line, i) => (
                  <span key={i} className="table-row">
                    <span className="table-cell select-none pr-4 text-right text-uwas-text-muted/40">{i + 1}</span>
                    <span className="table-cell">{line}{'\n'}</span>
                  </span>
                ))
              : code}
          </code>
        </pre>
      </div>
    </div>
  )
}
