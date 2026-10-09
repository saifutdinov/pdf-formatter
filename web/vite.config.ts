import { cpSync } from 'node:fs'
import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'

// pdf.js loads these at run time: decoders for JPEG 2000, JBIG2 and ICC
// colours, CMaps for CJK fonts, the standard 14 fonts and a CMYK profile.
const pdfjsData = ['cmaps', 'standard_fonts', 'wasm', 'iccs']

export default defineConfig({
  plugins: [
    preact(),
    {
      name: 'pdfjs-data',
      apply: 'build',
      closeBundle() {
        for (const d of pdfjsData) cpSync(`node_modules/pdfjs-dist/${d}`, `dist/pdfjs/${d}`, { recursive: true })
      },
    },
  ],
  server: {
    proxy: { '/api': process.env.API_URL ?? 'http://127.0.0.1:8080' },
  },
})
