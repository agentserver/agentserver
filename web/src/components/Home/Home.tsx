import { HomeNav } from './HomeNav'
import { HomeHero } from './HomeHero'
import { HomeQuote } from './HomeQuote'
import { HomePillars } from './HomePillars'
import { HomeCompare } from './HomeCompare'
import { HomeOnboarding } from './HomeOnboarding'
import { HomeFinalCTA } from './HomeFinalCTA'
import { HomeFooter } from './HomeFooter'

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
        <HomeFinalCTA />
      </main>
      <HomeFooter />
    </div>
  )
}
