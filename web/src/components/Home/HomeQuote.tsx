import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeQuote() {
  const t = useT(homeStrings)
  // Split the quote so 'conductor' and 'orchestrator' can be highlighted.
  const accent = (text: string) => (
    <span className="text-[var(--home-accent)] font-medium">{text}</span>
  )
  return (
    <section className="py-16 border-l-2 border-[var(--home-accent)] pl-6">
      <blockquote className="text-lg lg:text-xl text-[var(--foreground)] leading-relaxed max-w-3xl">
        "Once you juggle 10+ agents across machines, you stop being a {accent('conductor')} and become an {accent('orchestrator')}."
      </blockquote>
      <p className="mt-3 text-sm text-[var(--muted-foreground)] font-mono">
        {t('quote.attrib')}
      </p>
      <p className="mt-2 text-sm text-[var(--muted-foreground)]">
        {t('quote.caption')}
      </p>
    </section>
  )
}
