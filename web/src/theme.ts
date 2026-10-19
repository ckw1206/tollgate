// Theme resolution, shared by the console (useTheme in Layout.tsx) and the
// pre-paint script in index.html, which carries an inlined copy: it must run
// before the bundle loads, so the two implementations stay in sync by hand.
//
// An explicit pick ('light' | 'dark', stored in localStorage['tollgate.theme'])
// wins; System is stored by removing the key, so anything other than the two
// picks is treated as "no pick" and the OS preference decides.

export type ThemePref = 'system' | 'dark' | 'light'

export const THEME_KEY = 'tollgate.theme'

export const resolveMode = (stored: string | null, prefersDark: boolean): 'dark' | 'light' =>
  stored === 'light' || stored === 'dark' ? stored : prefersDark ? 'dark' : 'light'

// The stored preference, or 'system' when there is no pick (or storage is
// blocked). Only 'light'/'dark' count as a pick — System removes the key, so
// anything else is garbage and falls back to the OS.
export const storedPref = (): ThemePref => {
  try {
    const v = localStorage.getItem(THEME_KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}
