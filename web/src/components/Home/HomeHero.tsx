import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeHero() {
  const t = useT(homeStrings)
  const ref = useRef<HTMLDivElement | null>(null)
  const [started, setStarted] = useState(false)

  useEffect(() => {
    if (started) return
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reduce) {
      setStarted(true) // eslint-disable-line react-hooks/set-state-in-effect
      return
    }
    const node = ref.current
    if (!node) return
    const obs = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          setStarted(true)
          obs.disconnect()
          break
        }
      }
    }, { threshold: 0.25 })
    obs.observe(node)
    return () => obs.disconnect()
  }, [started])

  return (
    <section ref={ref} className="pt-12 pb-20" data-started={started ? 'true' : 'false'}>
      <div className="grid lg:grid-cols-[1.1fr_0.9fr] gap-10 items-start">
        {/* Left: heading + CTAs */}
        <div>
          <h1 className="text-5xl lg:text-6xl font-semibold tracking-tight leading-[1.1]">
            {t('hero.h1')}
          </h1>
          <p className="mt-6 text-base lg:text-lg text-[var(--muted-foreground)] leading-relaxed max-w-xl">
            {t('hero.sub')}
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Link
              to="/login"
              className="font-mono text-sm px-4 py-2 rounded-md bg-[var(--home-accent)] text-[var(--home-accent-fg)] hover:opacity-90"
            >
              {t('hero.cta.primary')}
            </Link>
            <a
              href="https://github.com/agentserver/agentserver"
              target="_blank"
              rel="noopener noreferrer"
              className="font-mono text-sm px-4 py-2 rounded-md border border-[var(--border)] hover:opacity-90"
            >
              {t('hero.cta.secondary')}
            </a>
            <span className="font-mono text-[10px] px-2 py-1 rounded border border-[var(--home-accent)]/40 text-[var(--home-accent)]">
              v{__APP_VERSION__} · {t('hero.chip.online')}
            </span>
          </div>
        </div>

        {/* Right: three-pane animated demo */}
        <div
          role="img"
          aria-label={t('hero.sub')}
          className="hero-demo grid grid-cols-[1fr_1fr] gap-3"
        >
          {/* Two terminal panes stacked in the left column */}
          <div className="flex flex-col gap-3">
            <Term
              title={t('hero.term.macbook')}
              lines={[
                { delay: 400,  text: '$ codex relay "把 experiment_0521.csv', kind: 'cmd' },
                { delay: 700,  text: '   在沙箱里画成图发我"',                kind: 'cmd' },
                { delay: 1100, text: '▸ found experiment_0521.csv (124MB)',  kind: 'dim' },
                { delay: 1500, text: '▸ relaying to cloud-sandbox-7…',       kind: 'dim' },
              ]}
            />
            <Term
              title={t('hero.term.sandbox')}
              lines={[
                { delay: 3000, text: '▸ pandas: 5 conditions × 12 trials',  kind: 'dim' },
                { delay: 3400, text: '▸ matplotlib: boxplot + error bars',  kind: 'dim' },
                { delay: 4400, text: '✓ saved plot.png (412KB)',            kind: 'ok'  },
                { delay: 4900, text: '▸ uploading via imbridge…',           kind: 'dim' },
              ]}
            />
          </div>

          {/* WeChat pane */}
          <ChatPane t={t} />
        </div>
      </div>

      <style>{`
        .hero-demo .ln,
        .hero-demo .bubble {
          opacity: 0;
          transform: translateY(4px);
        }
        .hero-demo[data-started="true"] .ln {
          animation: heroFadeIn 280ms ease-out forwards;
        }
        .hero-demo[data-started="true"] .bubble {
          animation: heroBubble 220ms ease-out forwards;
        }
        @keyframes heroFadeIn {
          to { opacity: 1; transform: translateY(0); }
        }
        @keyframes heroBubble {
          to { opacity: 1; transform: translateY(0); }
        }
        @media (prefers-reduced-motion: reduce) {
          .hero-demo .ln,
          .hero-demo .bubble {
            opacity: 1 !important;
            transform: none !important;
            animation: none !important;
          }
        }
      `}</style>
    </section>
  )
}

type TermLine = { delay: number; text: string; kind: 'cmd' | 'dim' | 'ok' }

function Term({ title, lines }: { title: string; lines: TermLine[] }) {
  return (
    <div className="rounded-md border border-[var(--home-term-border)] bg-[var(--home-term-bg)] font-mono text-[11px] overflow-hidden">
      <div className="px-3 py-1.5 border-b border-[var(--home-term-border)] text-[var(--home-term-dim)]">
        {title}
      </div>
      <div className="px-3 py-2 space-y-1">
        {lines.map((ln, i) => (
          <div
            key={i}
            className="ln"
            style={{ animationDelay: `${ln.delay}ms` }}
          >
            <span className={
              ln.kind === 'ok'  ? 'text-[var(--home-accent)]' :
              ln.kind === 'dim' ? 'text-[var(--home-term-dim)]' :
                                  'text-[var(--home-term-fg)]'
            }>
              {ln.text}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

function ChatPane({ t }: { t: (k: string) => string }) {
  type Bubble = { delay: number; who: 'me' | 'bot'; text: string; thumb?: boolean }
  const bubbles: Bubble[] = [
    { delay: 5500, who: 'me',  text: t('hero.chat.user') },
    { delay: 6000, who: 'bot', text: t('hero.chat.bot1') },
    { delay: 7000, who: 'bot', text: t('hero.chat.bot2') },
    { delay: 8500, who: 'bot', text: t('hero.chat.bot3'), thumb: true },
  ]
  return (
    <div className="rounded-md border border-[var(--home-term-border)] bg-[#ededed] dark:bg-[#1a1a1a] overflow-hidden">
      <div className="px-3 py-1.5 border-b border-[var(--home-term-border)] text-xs font-medium text-[var(--home-term-fg)]">
        {t('hero.chat.title')}
      </div>
      <div role="log" aria-live="polite" className="px-3 py-3 space-y-2 min-h-[260px]">
        {bubbles.map((b, i) => (
          <div
            key={i}
            className={`bubble flex ${b.who === 'me' ? 'justify-end' : 'justify-start'}`}
            style={{ animationDelay: `${b.delay}ms` }}
          >
            <div className={
              'max-w-[80%] rounded-md px-3 py-1.5 text-xs leading-relaxed ' +
              (b.who === 'me'
                ? 'bg-[#95ec69] text-black'
                : 'bg-white text-black dark:bg-[#2a2a2a] dark:text-white')
            }>
              {b.text}
              {b.thumb && (
                <div className="mt-1.5 h-12 w-full rounded bg-gradient-to-br from-[var(--home-accent)]/30 to-[var(--home-accent)]/10 border border-[var(--home-accent)]/40 flex items-center justify-center text-[10px] text-[var(--home-term-dim)]">
                  plot.png · 412KB
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
