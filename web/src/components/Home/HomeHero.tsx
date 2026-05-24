import { Link } from 'react-router-dom'
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeHero() {
  const t = useT(homeStrings)
  // Animation is pure CSS, auto-starts at page load. No JS gating — the hero
  // is always above the fold, and the prior IntersectionObserver / state-flip
  // approach silently failed in some production builds (data-started never
  // flipped, every .ln / .bubble stayed opacity:0 → invisible).
  return (
    <section className="pt-12 pb-20">
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
                { delay: 200,  text: '$ codex relay "在所有设备上找 thesis.pdf', kind: 'cmd' },
                { delay: 400,  text: '   比一下哪份最新"',                       kind: 'cmd' },
                { delay: 600,  text: '▸ 本机: ~/Documents/thesis.pdf',          kind: 'dim' },
                { delay: 800,  text: '✓ 2.1MB · 05-22 14:30',                   kind: 'ok'  },
              ]}
            />
            <Term
              title={t('hero.term.sandbox')}
              lines={[
                { delay: 1200, text: '▸ 收到广播查找请求',              kind: 'dim' },
                { delay: 1400, text: '▸ 本机: ~/work/thesis.pdf',       kind: 'dim' },
                { delay: 1800, text: '✓ 2.4MB · 05-24 10:42',           kind: 'ok'  },
                { delay: 2000, text: '▸ 已上报到 office-macbook',       kind: 'dim' },
              ]}
            />
          </div>

          {/* WeChat pane */}
          <ChatPane t={t} />
        </div>
      </div>

      <style>{`
        .hero-demo .ln {
          opacity: 0;
          transform: translateY(4px);
          animation: heroFadeIn 280ms ease-out forwards;
        }
        .hero-demo .bubble {
          opacity: 0;
          transform: translateY(4px);
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
            opacity: 1;
            transform: none;
            animation: none;
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
    { delay: 600,  who: 'me',  text: t('hero.chat.user') },
    { delay: 1000, who: 'bot', text: t('hero.chat.bot1') },
    { delay: 2200, who: 'bot', text: t('hero.chat.bot2') },
    { delay: 3200, who: 'bot', text: t('hero.chat.bot3'), thumb: true },
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
                  thesis.pdf · cloud-sandbox-7 · 05-24
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
