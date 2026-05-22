import { Link } from 'react-router-dom'
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeFinalCTA() {
  const t = useT(homeStrings)
  return (
    <section className="my-20 py-16 border-y-2 border-[var(--home-accent)]">
      <div className="text-center">
        <h2 className="text-3xl lg:text-4xl font-semibold tracking-tight max-w-2xl mx-auto">
          {t('cta.heading')}
        </h2>
        <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
          <Link
            to="/login"
            className="font-mono text-sm px-5 py-2.5 rounded-md bg-[var(--home-accent)] text-[var(--home-accent-fg)] hover:opacity-90"
          >
            {t('cta.primary')}
          </Link>
          <a
            href="https://github.com/agentserver/agentserver#self-hosting"
            target="_blank"
            rel="noopener noreferrer"
            className="font-mono text-sm px-5 py-2.5 rounded-md border border-[var(--border)] hover:opacity-90"
          >
            {t('cta.secondary')}
          </a>
        </div>
      </div>
    </section>
  )
}
