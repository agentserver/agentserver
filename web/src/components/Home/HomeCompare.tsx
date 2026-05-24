import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

type Row = { tool: string; local: string; cloud: string; peer: string; session: string; chat: string; us?: boolean }

const rows: Row[] = [
  { tool: 'OpenClaw / Claude Code Remote', local: '1',      cloud: '—',      peer: '—', session: '—',         chat: '✓' },
  { tool: 'Claude Code on the web',        local: '—',      cloud: '✓',      peer: '—', session: '—',         chat: '—' },
  { tool: 'CC Agent Teams',                local: '—',      cloud: '✓(sub)', peer: '—', session: '—',         chat: '—' },
  { tool: 'agentserver',                   local: '✓ many', cloud: '✓',      peer: '✓', session: '✓ any front-end', chat: '✓', us: true },
]

export function HomeCompare() {
  const t = useT(homeStrings)
  return (
    <section id="compare" className="py-20">
      <h2 className="font-mono text-xs tracking-[0.2em] text-[var(--muted-foreground)] uppercase mb-8">
        {t('compare.heading')}
      </h2>
      <div className="overflow-x-auto">
        <table className="w-full font-mono text-xs border border-[var(--home-term-border)]">
          <caption className="sr-only">{t('compare.heading')}</caption>
          <thead>
            <tr className="bg-[var(--card)] text-[var(--home-accent)]">
              <th scope="col" className="text-left p-3 font-medium">{t('compare.col.tool')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.local')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.cloud')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.peer')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.session')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.chat')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr
                key={r.tool}
                className={r.us
                  ? 'bg-[var(--home-grid)] border-l-2 border-[var(--home-accent)]'
                  : 'border-t border-[var(--home-term-border)]'
                }
              >
                <th scope="row" className={`text-left p-3 font-normal ${r.us ? 'text-[var(--home-accent)] font-medium' : ''}`}>
                  {r.us ? `▸ ${r.tool}` : r.tool}
                </th>
                <td className="text-center p-3">{r.local}</td>
                <td className="text-center p-3">{r.cloud}</td>
                <td className="text-center p-3">{r.peer}</td>
                <td className="text-center p-3">{r.session}</td>
                <td className="text-center p-3">{r.chat}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-4 text-sm text-[var(--muted-foreground)]">{t('compare.caption')}</p>
    </section>
  )
}
