// Server icon upload: validate a picked file, then center-crop and scale it to a 256×256 PNG
// data URL (smaller squares are upscaled) that PUT /console/api/v1/settings stores in server_icon.

export const ICON_TYPES = ['image/png', 'image/jpeg', 'image/webp']
const MAX_BYTES = 256 * 1024
const MIN_SIDE = 128
const OUT_SIDE = 256

// The declared MIME type must match the file's first bytes.
function magicMatches(type: string, b: Uint8Array) {
  const ascii = (from: number, to: number) => String.fromCharCode(...b.slice(from, to))
  switch (type) {
    case 'image/png':
      return [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a].every((v, i) => b[i] === v)
    case 'image/jpeg':
      return b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff
    case 'image/webp':
      return ascii(0, 4) === 'RIFF' && ascii(8, 12) === 'WEBP'
  }
  return false
}

export async function iconFromFile(f: File): Promise<string> {
  if (!ICON_TYPES.includes(f.type)) throw new Error(`${f.name} isn't supported. Use PNG, JPEG or WebP.`)
  if (f.size > MAX_BYTES) throw new Error('Icon must be 256 KB or smaller.')
  if (!magicMatches(f.type, new Uint8Array(await f.slice(0, 12).arrayBuffer()))) throw new Error(`${f.name} isn't a valid PNG, JPEG or WebP image.`)

  let bmp: ImageBitmap
  try {
    bmp = await createImageBitmap(f)
  } catch {
    throw new Error(`${f.name} isn't a valid PNG, JPEG or WebP image.`)
  }
  const { width: w, height: h } = bmp
  try {
    if (Math.abs(w - h) / Math.max(w, h) > 0.05) throw new Error('Icon must be square.')
    if (Math.min(w, h) < MIN_SIDE) throw new Error(`Icon must be at least ${MIN_SIDE}×${MIN_SIDE}.`)

    // Center-crop the (nearly) square image and scale it to 256×256.
    const side = Math.min(w, h)
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = OUT_SIDE
    const ctx = canvas.getContext('2d')
    if (!ctx) throw new Error("This browser can't process images.")
    ctx.imageSmoothingQuality = 'high'
    ctx.drawImage(bmp, (w - side) / 2, (h - side) / 2, side, side, 0, 0, OUT_SIDE, OUT_SIDE)
    const url = canvas.toDataURL('image/png')
    // A 256×256 PNG of pure noise can exceed the server's cap; say so instead of a raw 400.
    if (((url.length - url.indexOf(',') - 1) * 3) / 4 > MAX_BYTES) throw new Error('Icon is too detailed to fit in 256 KB. Try a simpler image.')
    return url
  } finally {
    bmp.close()
  }
}
