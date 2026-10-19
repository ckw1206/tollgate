// The polyline of a Sparkline (components/ui.tsx): `values` min-max
// normalised into a w×h box with a 1px pad. Fewer than 2 values -> ''
// (render nothing); a flat series -> one mid-height line, so the max - min
// division never produces a NaN.

export const sparkPoints = (values: number[], w = 100, h = 24): string => {
  const pad = 1
  if (values.length < 2) return ''
  const max = Math.max(...values)
  const min = Math.min(...values)
  const y = (v: number) => (min === max ? (h - 2 * pad) / 2 + pad : h - pad - ((v - min) / (max - min)) * (h - 2 * pad))
  return values.map((v, i) => `${(((i / (values.length - 1)) * (w - 2 * pad) + pad).toFixed(2))},${y(v).toFixed(2)}`).join(' ')
}
