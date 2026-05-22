import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeFooter() {
  const t = useT(homeStrings)
  return (
    <footer className="border-t border-[var(--border)] py-10 mt-10">
      <div className="grid md:grid-cols-3 gap-8 text-sm">
        <div>
          <p className="font-mono font-semibold mb-3">{t('footer.col1.title')}</p>
          <ul className="space-y-1 text-[var(--muted-foreground)]">
            <li><a className="hover:text-[var(--foreground)]" href="https://agentserver.dev" target="_blank" rel="noopener noreferrer">agentserver.dev</a></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver" target="_blank" rel="noopener noreferrer">GitHub</a></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver#readme" target="_blank" rel="noopener noreferrer">Docs</a></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver/releases" target="_blank" rel="noopener noreferrer">Changelog</a></li>
          </ul>
        </div>
        <div>
          <p className="font-mono font-semibold mb-3">{t('footer.col2.title')}</p>
          <ul className="space-y-1 text-[var(--muted-foreground)]">
            <li><span>{t('footer.col2.weixin')}</span></li>
            <li><span>{t('footer.col2.telegram')}</span></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver/issues" target="_blank" rel="noopener noreferrer">{t('footer.col2.issues')}</a></li>
          </ul>
        </div>
        <div>
          <p className="font-mono font-semibold mb-3">{t('footer.col3.title')}</p>
          <ul className="space-y-1 text-[var(--muted-foreground)]">
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">{t('footer.col3.license')}</a></li>
            <li><span>{t('footer.col3.privacy')}</span></li>
            <li><span>{t('footer.col3.contact')}</span></li>
          </ul>
        </div>
      </div>
      <p className="mt-8 text-center font-mono text-[10px] text-[var(--muted-foreground)]">
        v{__APP_VERSION__} · {__BUILD_COMMIT__} · built {__BUILD_DATE__}
      </p>
    </footer>
  )
}
