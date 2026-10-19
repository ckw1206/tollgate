// Toll-lane grid: perspective floor + ceiling converging on a gate, lanes flowing toward the viewer.
// Faithful port of the reference tollgate-grid.js (which exposed window.TollgateGrid.mount);
// the port exports `mount` instead. Colours are read from the CSS custom properties
// --primary / --background so the grid matches the page it sits on.
//
// mount(canvas, opts) -> destroy(). Destroy removes the window/document listeners,
// cancels the rAF and disconnects the ResizeObserver.

export interface GridOpts {
  horizon: number
  intensity: number
  flow: number
  ceiling: boolean
  traffic: boolean
  color: string
  bg: string
}

interface Car {
  lane: number
  z: number
  v: number
}

interface TrailPoint {
  x: number
  y: number
  life: number
}

interface Ripple {
  x: number
  y: number
  r: number
  a: number
}

const XN = 13
const XS = 0.5
const Z0 = 0.55
const Z1 = 26
const ZSTEP = 0.6
const NZ = 44
const S2 = 2 * 130 * 130

// `color-mix` of a colour with transparent, for canvas strokes/fills/gradient stops
// (Canvas 2D accepts color-mix() in current Chromium, Safari and Firefox). The reference
// script spelled these as colour literals; the port keeps every colour a token resolved
// from the themed page instead.
export const alpha = (color: string, pct: number): string => `color-mix(in srgb, ${color} ${pct}%, transparent)`

export function mount(canvas: HTMLCanvasElement, o?: Partial<GridOpts>): () => void {
  const opts = { horizon: 0.5, intensity: 1, flow: 1, ceiling: true, traffic: true, color: '', bg: '', ...o }
  const ctx = canvas.getContext('2d')!
  const off = document.createElement('canvas')
  const octx = off.getContext('2d')!
  const reduce = window.matchMedia ? window.matchMedia('(prefers-reduced-motion: reduce)').matches : false
  // The themed shell always defines these tokens; the keyword fallbacks are a last resort
  // for a canvas mounted outside one (the reference's were hard-coded colours).
  const computed = getComputedStyle(canvas)
  const fg = computed.getPropertyValue('--foreground').trim() || 'white'
  const accent = opts.color || computed.getPropertyValue('--primary').trim() || fg
  const bg = opts.bg || computed.getPropertyValue('--background').trim() || 'black'
  let W = 0, H = 0, dpr = 1, raf = 0, alive = true
  const m = { x: 0, y: 0, tx: 0, ty: 0, a: 0, on: false, seen: false }
  const trail: TrailPoint[] = []
  const ripples: Ripple[] = []
  const cars: Car[] = Array.from({ length: 16 }, () => ({
    lane: (Math.floor(Math.random() * XN * 2) - XN + 0.5) * XS,
    z: Z0 + Math.random() * (Z1 - Z0),
    v: 0.04 + Math.random() * 0.06,
  }))

  function resize() {
    const r = canvas.getBoundingClientRect()
    dpr = Math.min(2, window.devicePixelRatio || 1)
    W = r.width
    H = r.height
    for (const c of [canvas, off]) {
      c.width = Math.max(1, W * dpr)
      c.height = Math.max(1, H * dpr)
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    octx.setTransform(dpr, 0, 0, dpr, 0, 0)
  }

  const ro = new ResizeObserver(resize)
  ro.observe(canvas)
  resize()

  const local = (e: PointerEvent): [number, number] => {
    const r = canvas.getBoundingClientRect()
    return [e.clientX - r.left, e.clientY - r.top]
  }
  const onMove = (e: PointerEvent) => {
    const [x, y] = local(e)
    m.tx = x
    m.ty = y
    if (!m.seen) {
      m.x = x
      m.y = y
      m.seen = true
    }
    m.on = y >= 0 && y <= H && x >= 0 && x <= W
  }
  const onLeave = () => {
    m.on = false
  }
  const onDown = (e: PointerEvent) => {
    const [x, y] = local(e)
    if (y < 0 || y > H) return
    ripples.push({ x, y, r: 0, a: 1 })
    if (ripples.length > 5) ripples.shift()
  }
  window.addEventListener('pointermove', onMove, { passive: true })
  window.addEventListener('pointerdown', onDown, { passive: true })
  document.addEventListener('pointerleave', onLeave)

  function lift(sx: number, sy: number) {
    let d = 0
    if (m.a > 0.01) {
      const dx = sx - m.x, dy = sy - m.y
      d += m.a * 44 * Math.exp(-(dx * dx + dy * dy) / S2)
    }
    for (const r of ripples) {
      const dx = sx - r.x, dy = sy - r.y
      const k = Math.sqrt(dx * dx + dy * dy) - r.r
      d += r.a * 26 * Math.exp(-(k * k) / 1800)
    }
    return d
  }

  let t = 0, last = performance.now()

  function surface(path: Path2D, sign: number, cx: number, cy: number, F: number, phase: number, wave: number) {
    const pt = (x: number, z: number): [number, number] => {
      const y = sign * (1 - wave * Math.sin(x * 0.55 + t * 0.6) * Math.cos(z * 0.32 - t * 0.45))
      const sx = cx + (x / z) * F
      let sy = cy - (y / z) * F
      sy += sign * lift(sx, sy)
      return [sx, sy]
    }
    for (let i = -XN; i <= XN; i++) {
      const x = i * XS
      for (let k = 0; k <= NZ; k++) {
        const z = Z0 * Math.pow(Z1 / Z0, k / NZ)
        const [sx, sy] = pt(x, z)
        if (k) path.lineTo(sx, sy)
        else path.moveTo(sx, sy)
      }
    }
    for (let z = Z0 + (ZSTEP - phase); z < Z1; z += ZSTEP) {
      for (let i = -XN * 2; i <= XN * 2; i++) {
        const [sx, sy] = pt((i * XS) / 2, z)
        if (i > -XN * 2) path.lineTo(sx, sy)
        else path.moveTo(sx, sy)
      }
    }
  }

  function frame(now: number) {
    if (!alive) return
    const dt = Math.min(50, now - last)
    last = now
    t += (dt / 1000) * (reduce ? 0.3 : 1)
    m.x += (m.tx - m.x) * 0.14
    m.y += (m.ty - m.y) * 0.14
    m.a += ((m.on ? 1 : 0) - m.a) * 0.06
    if (m.on && !reduce) {
      trail.push({ x: m.x, y: m.y, life: 1 })
      if (trail.length > 40) trail.shift()
    }
    for (const p of trail) p.life -= 0.022
    while (trail.length && trail[0].life <= 0) trail.shift()
    for (const r of ripples) {
      r.r += dt * 0.55
      r.a -= dt / 1600
    }
    while (ripples.length && ripples[0].a <= 0) ripples.shift()
    const cx = W / 2
    const cy = H * opts.horizon
    const F = Math.max(W, H) * 0.42
    const phase = reduce ? 0 : (t * 0.9 * opts.flow) % ZSTEP
    const wave = 0.07
    const floor = new Path2D()
    const ceil = new Path2D()
    surface(floor, -1, cx, cy, F, phase, wave)
    if (opts.ceiling) surface(ceil, 1, cx, cy, F, phase, wave)
    ctx.clearRect(0, 0, W, H)
    ctx.lineWidth = 1
    ctx.strokeStyle = alpha(fg, 15 * opts.intensity)
    ctx.stroke(floor)
    ctx.strokeStyle = alpha(fg, 7 * opts.intensity)
    if (opts.ceiling) ctx.stroke(ceil)
    if (opts.traffic && !reduce) {
      ctx.strokeStyle = accent
      ctx.lineCap = 'round'
      for (const c of cars) {
        c.z -= (c.v * dt) / 16 * opts.flow
        if (c.z < Z0) {
          c.z = Z1
          c.lane = (Math.floor(Math.random() * XN * 2) - XN + 0.5) * XS
        }
        const z2 = c.z + 0.9
        const a = Math.min(1, (Z1 - c.z) / 6) * Math.min(1, (c.z - Z0) / 1.5)
        const s1x = cx + (c.lane / c.z) * F
        const s1y = cy + F / c.z
        const s2x = cx + (c.lane / z2) * F
        const s2y = cy + F / z2
        ctx.globalAlpha = 0.55 * a * opts.intensity
        ctx.lineWidth = Math.max(1, 3 / c.z)
        ctx.beginPath()
        ctx.moveTo(s1x, s1y - lift(s1x, s1y))
        ctx.lineTo(s2x, s2y - lift(s2x, s2y))
        ctx.stroke()
      }
      ctx.globalAlpha = 1
      ctx.lineWidth = 1
    }
    octx.globalCompositeOperation = 'source-over'
    octx.clearRect(0, 0, W, H)
    const blob = (x: number, y: number, r: number, a: number) => {
      const g = octx.createRadialGradient(x, y, 0, x, y, r)
      g.addColorStop(0, alpha(fg, a * 100))
      g.addColorStop(1, alpha(fg, 0))
      octx.fillStyle = g
      octx.fillRect(x - r, y - r, r * 2, r * 2)
    }
    for (const p of trail) blob(p.x, p.y, 70 + 90 * p.life, 0.35 * p.life)
    if (m.a > 0.01) blob(m.x, m.y, 210, 0.95 * m.a)
    for (const r of ripples) {
      octx.lineWidth = 50
      octx.strokeStyle = alpha(fg, 70 * r.a)
      octx.beginPath()
      octx.arc(r.x, r.y, r.r, 0, Math.PI * 2)
      octx.stroke()
    }
    octx.globalCompositeOperation = 'source-in'
    octx.strokeStyle = accent
    octx.lineWidth = 1.25
    octx.stroke(floor)
    if (opts.ceiling) octx.stroke(ceil)
    ctx.drawImage(off, 0, 0, W, H)
    const fog = ctx.createRadialGradient(cx, cy, 0, cx, cy, Math.max(W, H) * 0.32)
    fog.addColorStop(0, bg)
    fog.addColorStop(0.35, bg)
    fog.addColorStop(1, alpha(bg, 0))
    ctx.fillStyle = fog
    ctx.fillRect(0, 0, W, H)
    raf = requestAnimationFrame(frame)
  }
  raf = requestAnimationFrame(frame)

  return () => {
    alive = false
    cancelAnimationFrame(raf)
    ro.disconnect()
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerdown', onDown)
    document.removeEventListener('pointerleave', onLeave)
  }
}
