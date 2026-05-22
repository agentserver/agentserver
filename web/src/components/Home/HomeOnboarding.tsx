import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

const steps = [
  { n: 1, titleKey: 'onb.s1.title', bodyKey: 'onb.s1.body' },
  { n: 2, titleKey: 'onb.s2.title', bodyKey: 'onb.s2.body' },
  { n: 3, titleKey: 'onb.s3.title', bodyKey: 'onb.s3.body' },
  { n: 4, titleKey: 'onb.s4.title', bodyKey: 'onb.s4.body' },
  { n: 5, titleKey: 'onb.s5.title', bodyKey: 'onb.s5.body' },
  { n: 6, titleKey: 'onb.s6.title', bodyKey: 'onb.s6.body', emphasized: true },
  { n: 7, titleKey: 'onb.s7.title', bodyKey: 'onb.s7.body' },
]

export function HomeOnboarding() {
  const t = useT(homeStrings)
  return (
    <section id="how" className="py-20">
      <h2 className="font-mono text-xs tracking-[0.2em] text-[var(--muted-foreground)] uppercase mb-8">
        {t('onb.heading')}
      </h2>
      <ol className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {steps.map((s) => (
          <li
            key={s.n}
            className={
              'rounded-lg border p-5 ' +
              (s.emphasized
                ? 'border-[var(--home-accent)] bg-[var(--home-grid)]'
                : 'border-[var(--border)] bg-[var(--card)]')
            }
          >
            <div className={
              'inline-flex items-center justify-center h-7 w-7 rounded-full font-mono text-xs mb-3 ' +
              (s.emphasized
                ? 'bg-[var(--home-accent)] text-[var(--home-accent-fg)]'
                : 'bg-[var(--secondary)] text-[var(--secondary-foreground)]')
            }>
              {s.n}
            </div>
            <h3 className={`text-sm font-semibold mb-1 ${s.emphasized ? 'text-[var(--home-accent)]' : ''}`}>
              {t(s.titleKey)}
            </h3>
            <p className="text-xs text-[var(--muted-foreground)] leading-relaxed">{t(s.bodyKey)}</p>
          </li>
        ))}
      </ol>
      <a
        href="https://github.com/agentserver/agentserver#readme"
        target="_blank"
        rel="noopener noreferrer"
        className="inline-block mt-6 font-mono text-xs text-[var(--home-accent)] hover:underline"
      >
        {t('onb.docs')}
      </a>
    </section>
  )
}
