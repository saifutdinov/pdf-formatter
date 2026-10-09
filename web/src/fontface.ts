const loaded = new Map<string, Promise<string>>()

/** Loads a library font into the page and returns its CSS family name. */
export function useLibFont(id: string): string {
  if (!id) return 'sans-serif'
  const family = `lib-${id}`
  if (!loaded.has(id)) {
    const face = new FontFace(family, `url(/api/fonts/${id}/file)`)
    loaded.set(
      id,
      face.load().then((f) => {
        document.fonts.add(f)
        return family
      }),
    )
  }
  return `"${family}", sans-serif`
}
