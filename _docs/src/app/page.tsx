import Link from 'next/link';

export default function HomePage() {
  return (
    <main className="flex flex-1 flex-col items-center justify-center px-4 py-16 text-center">
      <div className="max-w-3xl">
        <div className="mb-6 inline-flex items-center rounded-full border px-4 py-1.5 text-sm">
          <span className="mr-2">🏄</span>
          <span>v0.1.0 - Fast, Simple, Go</span>
        </div>

        <h1 className="mb-6 text-5xl font-bold tracking-tight md:text-6xl">
          Build web apps with{' '}
          <span className="bg-gradient-to-r from-green-500 to-emerald-500 bg-clip-text text-transparent">
            Surf
          </span>
        </h1>

        <p className="mb-8 text-lg text-muted-foreground md:text-xl">
          A lightweight, high-performance HTTP web framework for Go with
          radix tree routing, built-in middleware, and structured logging.
        </p>

        <div className="flex flex-col gap-4 sm:flex-row sm:justify-center">
          <Link
            href="/docs"
            className="inline-flex items-center justify-center rounded-lg bg-primary px-6 py-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
          >
            Get Started
            <svg className="ml-2 h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
            </svg>
          </Link>
          <Link
            href="https://github.com/getangry/surf"
            className="inline-flex items-center justify-center rounded-lg border px-6 py-3 text-sm font-medium transition-colors hover:bg-muted"
          >
            <svg className="mr-2 h-4 w-4" fill="currentColor" viewBox="0 0 24 24">
              <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/>
            </svg>
            GitHub
          </Link>
        </div>

        <div className="mt-16 grid gap-6 sm:grid-cols-3">
          <div className="rounded-lg border p-6 text-left transition-all hover:-translate-y-1 hover:shadow-lg">
            <div className="mb-3 inline-flex h-10 w-10 items-center justify-center rounded-lg bg-green-100 text-green-600 dark:bg-green-900/30">
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
              </svg>
            </div>
            <h3 className="mb-2 font-semibold">Fast Routing</h3>
            <p className="text-sm text-muted-foreground">
              Radix tree router with O(log n) lookups. 73x faster than linear search.
            </p>
          </div>

          <div className="rounded-lg border p-6 text-left transition-all hover:-translate-y-1 hover:shadow-lg">
            <div className="mb-3 inline-flex h-10 w-10 items-center justify-center rounded-lg bg-blue-100 text-blue-600 dark:bg-blue-900/30">
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4" />
              </svg>
            </div>
            <h3 className="mb-2 font-semibold">Built-in Middleware</h3>
            <p className="text-sm text-muted-foreground">
              CORS, rate limiting, recovery, timeout, and gzip compression out of the box.
            </p>
          </div>

          <div className="rounded-lg border p-6 text-left transition-all hover:-translate-y-1 hover:shadow-lg">
            <div className="mb-3 inline-flex h-10 w-10 items-center justify-center rounded-lg bg-purple-100 text-purple-600 dark:bg-purple-900/30">
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
              </svg>
            </div>
            <h3 className="mb-2 font-semibold">Production Ready</h3>
            <p className="text-sm text-muted-foreground">
              Graceful shutdown, structured logging, and thread-safe design.
            </p>
          </div>
        </div>
      </div>
    </main>
  );
}
