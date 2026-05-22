import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

type Pillar = {
  emoji: string
  titleKey: string
  bodyKey: string
}

const pillars: Pillar[] = [
  { emoji: '📱', titleKey: 'pillars.pocket.title',    bodyKey: 'pillars.pocket.body' },
  { emoji: '🌐', titleKey: 'pillars.workspace.title', bodyKey: 'pillars.workspace.body' },
  { emoji: '🔌', titleKey: 'pillars.tunnel.title',    bodyKey: 'pillars.tunnel.body' },
]

export function HomePillars() {
  const t = useT(homeStrings)
  return (
    <section id="why" className="py-20">
      <h2 className="font-mono text-xs tracking-[0.2em] text-[var(--muted-foreground)] uppercase mb-8">
        {t('pillars.heading')}
      </h2>
      <div className="grid md:grid-cols-3 gap-4">
        {pillars.map((p) => (
          <article
            key={p.titleKey}
            className="rounded-lg border border-[var(--border)] p-6 bg-[var(--card)]"
          >
            <div className="text-3xl mb-3" aria-hidden="true">{p.emoji}</div>
            <h3 className="text-lg font-semibold mb-2">{t(p.titleKey)}</h3>
            <p className="text-sm text-[var(--muted-foreground)] leading-relaxed">{t(p.bodyKey)}</p>
          </article>
        ))}
      </div>
      <p className="mt-6 font-mono text-xs text-[var(--muted-foreground)] leading-relaxed">
        {t('pillars.also')}
      </p>
    </section>
  )
}
