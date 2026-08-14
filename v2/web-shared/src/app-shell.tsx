import * as DialogPrimitive from "@radix-ui/react-dialog"
import { Check, ChevronsUpDown, Languages, Laptop, Menu, Moon, PanelLeftClose, PanelLeftOpen, Search, Sun, UserRound } from "lucide-react"
import { useEffect, useMemo, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useLocale, type Locale } from "./i18n"
import { useTheme, type Theme } from "./theme"
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger, Input, Tooltip } from "./ui"
import { cn } from "./utils"

export interface CommandItem {
  id: string
  label: string
  keywords?: string
  icon?: ReactNode
  run: () => void
}

export function AppShell({ sidebar, children, commands = [], accountLabel, className, onSignOut }: {
  sidebar: ReactNode
  children: ReactNode
  commands?: CommandItem[]
  accountLabel?: string
  className?: string
  onSignOut: () => void
}) {
  const { t } = useTranslation()
  const [collapsed, setCollapsed] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)

  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault()
        setCommandOpen(true)
      }
    }
    window.addEventListener("keydown", handler)
    return () => window.removeEventListener("keydown", handler)
  }, [])

  return <div className={cn("app-shell", className, collapsed && "sidebar-collapsed", mobileOpen && "sidebar-mobile-open")}>
    <aside className="app-sidebar" aria-label="Application navigation" onClick={() => setMobileOpen(false)}>
      <div className="sidebar-collapse-row">
        <Tooltip label={collapsed ? t("common.expand") : t("common.collapse")}>
          <Button variant="ghost" size="icon" onClick={() => setCollapsed((value) => !value)} aria-label={collapsed ? t("common.expand") : t("common.collapse")}>
            {collapsed ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
          </Button>
        </Tooltip>
      </div>
      <div className="sidebar-scroll">{sidebar}</div>
      <AccountMenu label={accountLabel} onSignOut={onSignOut} />
    </aside>
    <button className="sidebar-scrim" type="button" aria-label={t("common.close")} onClick={() => setMobileOpen(false)} />
    <main className="app-main">
      <div className="mobile-header"><Button variant="ghost" size="icon" onClick={() => setMobileOpen(true)} aria-label={t("common.menu")}><Menu size={18} /></Button></div>
      {children}
    </main>
    <CommandPalette open={commandOpen} onOpenChange={setCommandOpen} items={commands} />
  </div>
}

export function SidebarSearchButton({ onClick }: { onClick?: () => void }) {
  const { t } = useTranslation()
  const trigger = () => {
    if (onClick) onClick()
    else window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true }))
  }
  return <button className="sidebar-nav-button sidebar-search" type="button" onClick={trigger}>
    <Search size={17} /><span className="sidebar-copy">{t("common.search")}</span><kbd className="sidebar-copy">{t("common.commandHint")}</kbd>
  </button>
}

export function SidebarNavButton({ icon, label, active, onClick, trailing }: { icon: ReactNode; label: string; active?: boolean; onClick: () => void; trailing?: ReactNode }) {
  return <Tooltip label={label}><button className={cn("sidebar-nav-button", active && "active")} type="button" onClick={onClick}>
    <span className="sidebar-nav-icon">{icon}</span><span className="sidebar-copy sidebar-nav-label">{label}</span>{trailing ? <span className="sidebar-copy sidebar-nav-trailing">{trailing}</span> : null}
  </button></Tooltip>
}

export function SidebarSection({ label, children }: { label?: string; children: ReactNode }) {
  return <section className="sidebar-section">{label ? <div className="sidebar-section-label sidebar-copy">{label}</div> : null}{children}</section>
}

export function SidebarBrand({ mark = "AS", title, subtitle, onClick }: { mark?: string; title: string; subtitle?: string; onClick?: () => void }) {
  return <button className="sidebar-brand" type="button" onClick={onClick}><span className="brand-mark">{mark}</span><span className="sidebar-copy brand-copy"><strong>{title}</strong>{subtitle ? <small>{subtitle}</small> : null}</span></button>
}

export function WorkspaceSwitcher({ value, label, items, onChange }: { value: string; label: string; items: { id: string; name: string }[]; onChange: (id: string) => void }) {
  const selected = items.find((item) => item.id === value)
  return <DropdownMenu><DropdownMenuTrigger asChild><button className="workspace-switcher" type="button">
    <span className="workspace-avatar">{(selected?.name ?? "W").slice(0, 1).toUpperCase()}</span>
    <span className="sidebar-copy workspace-switcher-copy"><small>{label}</small><strong>{selected?.name ?? label}</strong></span>
    <ChevronsUpDown className="sidebar-copy" size={15} />
  </button></DropdownMenuTrigger><DropdownMenuContent className="workspace-menu" align="start">
    <DropdownMenuLabel>{label}</DropdownMenuLabel>
    {items.map((item) => <DropdownMenuItem key={item.id} onSelect={() => onChange(item.id)}><span className="workspace-menu-mark">{item.name.slice(0, 1).toUpperCase()}</span><span>{item.name}</span>{item.id === value ? <Check size={14} /> : null}</DropdownMenuItem>)}
  </DropdownMenuContent></DropdownMenu>
}

function AccountMenu({ label, onSignOut }: { label: string | undefined; onSignOut: () => void }) {
  const { t } = useTranslation()
  const { theme, setTheme } = useTheme()
  const { locale, setLocale } = useLocale()
  return <div className="sidebar-account"><DropdownMenu><DropdownMenuTrigger asChild><button type="button" className="account-trigger">
    <span className="account-avatar"><UserRound size={16} /></span><span className="sidebar-copy account-copy"><strong>{label || t("common.account")}</strong><small>{t("common.signedIn")}</small></span><ChevronsUpDown className="sidebar-copy" size={15} />
  </button></DropdownMenuTrigger><DropdownMenuContent align="start" className="account-menu">
    <DropdownMenuLabel>{t("common.theme")}</DropdownMenuLabel>
    <div className="segmented-control" role="group" aria-label={t("common.theme")}>
      <ThemeButton value="light" active={theme === "light"} onClick={setTheme} icon={<Sun size={14} />} label={t("common.light")} />
      <ThemeButton value="dark" active={theme === "dark"} onClick={setTheme} icon={<Moon size={14} />} label={t("common.dark")} />
      <ThemeButton value="system" active={theme === "system"} onClick={setTheme} icon={<Laptop size={14} />} label={t("common.system")} />
    </div>
    <DropdownMenuSeparator />
    <DropdownMenuLabel>{t("common.language")}</DropdownMenuLabel>
    <DropdownMenuItem onSelect={() => setLocale("en-US")}><Languages size={15} />{t("common.english")} {locale === "en-US" ? <Check size={14} /> : null}</DropdownMenuItem>
    <DropdownMenuItem onSelect={() => setLocale("zh-CN")}><Languages size={15} />{t("common.chinese")} {locale === "zh-CN" ? <Check size={14} /> : null}</DropdownMenuItem>
    <DropdownMenuSeparator />
    <DropdownMenuItem onSelect={() => { window.location.href = "https://agent.byted.bps.dev/" }}>{t("nav.platform")}</DropdownMenuItem>
    <DropdownMenuItem onSelect={() => { window.location.href = "https://browser.byted.bps.dev/" }}>{t("nav.browser")}</DropdownMenuItem>
    <DropdownMenuSeparator />
    <DropdownMenuItem className="danger-item" onSelect={onSignOut}>{t("common.signOut")}</DropdownMenuItem>
  </DropdownMenuContent></DropdownMenu></div>
}

function ThemeButton({ value, active, onClick, icon, label }: { value: Theme; active: boolean; onClick: (theme: Theme) => void; icon: ReactNode; label: string }) {
  return <button type="button" className={cn(active && "active")} onClick={(event) => { event.preventDefault(); onClick(value) }} title={label}>{icon}</button>
}

function CommandPalette({ open, onOpenChange, items }: { open: boolean; onOpenChange: (open: boolean) => void; items: CommandItem[] }) {
  const { t } = useTranslation()
  const [query, setQuery] = useState("")
  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    if (!needle) return items
    return items.filter((item) => `${item.label} ${item.keywords ?? ""}`.toLocaleLowerCase().includes(needle))
  }, [items, query])
  return <DialogPrimitive.Root open={open} onOpenChange={(next) => { onOpenChange(next); if (!next) setQuery("") }}><DialogPrimitive.Portal>
    <DialogPrimitive.Overlay className="dialog-overlay command-overlay" />
    <DialogPrimitive.Content className="command-dialog"><DialogPrimitive.Title className="sr-only">{t("common.search")}</DialogPrimitive.Title>
      <div className="command-input"><Search size={18} /><Input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("common.search")} /></div>
      <div className="command-results">{filtered.length ? filtered.map((item) => <button key={item.id} type="button" onClick={() => { item.run(); onOpenChange(false) }}>{item.icon}<span>{item.label}</span></button>) : <div className="command-empty">{t("common.noResults")}</div>}</div>
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal></DialogPrimitive.Root>
}

export function SignedOutShell({ title, description, error, children }: { title: string; description: string; error?: string | undefined; children: ReactNode }) {
  return <main className="auth-page"><div className="auth-brand"><span className="brand-mark">AS</span><span>AgentServer</span></div><section className="auth-card"><h1>{title}</h1><p>{description}</p>{error ? <div className="error-banner">{error}</div> : null}{children}</section><div className="auth-grid" aria-hidden="true" /></main>
}
