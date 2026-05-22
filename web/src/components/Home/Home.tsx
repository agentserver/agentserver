import { HomeCompare } from './HomeCompare'
import { HomeHero } from './HomeHero'
import { HomeNav } from './HomeNav'
import { HomeOnboarding } from './HomeOnboarding'
import { HomePillars } from './HomePillars'
import { HomeQuote } from './HomeQuote'

export function Home() {
  return (
    <div data-theme="home" className="min-h-screen bg-[var(--background)] text-[var(--foreground)]">
      <HomeNav />
      <main className="mx-auto max-w-6xl px-6">
        <HomeHero />
        <HomeQuote />
        <HomePillars />
        <HomeCompare />
        <HomeOnboarding />
      </main>
    </div>
  )
}
