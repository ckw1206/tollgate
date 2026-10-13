import type { ReactNode } from 'react'

// AuthCard: the centered 384px card used by /login and /invite/:token (outside the Layout).
export default function AuthCard({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <div className="w-full max-w-[384px] rounded-md border border-border bg-card p-6">{children}</div>
    </div>
  )
}
