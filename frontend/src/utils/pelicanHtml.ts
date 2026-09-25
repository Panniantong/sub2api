// Scripts may animate inside the sandboxed iframe; the page cannot reach the network.
const PELICAN_CSP = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: blob:; media-src data: blob:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'">`

export function extractPelicanHtml(raw: string): string {
  let html = raw.trim()
  const fenced = html.match(/```(?:html|xml)?\s*([\s\S]*?)```/i)
  if (fenced?.[1]) html = fenced[1].trim()
  const lower = html.toLowerCase()
  const start = Math.min(...['<!doctype html', '<html', '<svg'].map((marker) => {
    const index = lower.indexOf(marker)
    return index < 0 ? Number.MAX_SAFE_INTEGER : index
  }))
  if (start !== Number.MAX_SAFE_INTEGER) html = html.slice(start).trim()
  if (!/<(?:!doctype\s+html|html|svg)[\s>]/i.test(html)) return ''
  const end = html.toLowerCase().lastIndexOf('</html>')
  if (end >= 0) html = html.slice(0, end + '</html>'.length)
  if (!/<html[\s>]/i.test(html) && /<svg[\s>]/i.test(html)) {
    // Bare SVG: centre it and scale it down to the frame instead of the default 8px page margin.
    html = `<html><head><meta charset="utf-8"><title>Pelican test</title><style>html,body{margin:0;height:100%}body{display:flex;align-items:center;justify-content:center}svg{max-width:100%;max-height:100%}</style></head><body>${html}</body></html>`
  }
  // A CSP <meta> only covers markup parsed after it, so it leads the document rather than
  // following the model's <head>, which a comment or an earlier script could precede.
  // The parser moves it into an implicit <head>; the model's own <html>/<head> tags merge in.
  return `<!doctype html>${PELICAN_CSP}${html.replace(/^<!doctype[^>]*>/i, '')}`
}
