import { gzipSync } from 'node:zlib'
import { readdir, readFile, stat } from 'node:fs/promises'
import path from 'node:path'
import { moduleEntryAssetURLs, resolveDistAssetPath, staticModuleAssetURLs } from './performance-budget-assets.mjs'

const distDir = path.resolve('dist')
const indexHTML = await readFile(path.join(distDir, 'index.html'), 'utf8')
const entryURLs = moduleEntryAssetURLs(indexHTML)
if (entryURLs.length !== 1) throw new Error(`expected one production module entry, found ${entryURLs.length}`)
const entryURL = entryURLs[0]

const entryPath = resolveDistAssetPath(distDir, entryURL)
const entry = await readFile(entryPath)
const entryRawBytes = entry.byteLength
const entryGzipBytes = gzipSync(entry, { level: 9 }).byteLength

// Vite moves shared entry dependencies into modulepreload files. Browsers fetch
// those files on the initial route, so checking index-*.js alone can undercount
// the real public boot payload and miss admin/detail sentinels in shared chunks.
const initialJavaScriptURLs = staticModuleAssetURLs(indexHTML)
if (!initialJavaScriptURLs.includes(entryURL)) {
  throw new Error('production entry is missing from the initial JavaScript graph')
}
const initialJavaScriptAssets = await Promise.all(initialJavaScriptURLs.map(async (url) => {
  const assetPath = resolveDistAssetPath(distDir, url)
  const content = await readFile(assetPath)
  return {
    file: path.basename(assetPath),
    content,
    rawBytes: content.byteLength,
    gzipBytes: gzipSync(content, { level: 9 }).byteLength,
  }
}))
const initialJavaScriptRawBytes = initialJavaScriptAssets.reduce((total, asset) => total + asset.rawBytes, 0)
const initialJavaScriptGzipBytes = initialJavaScriptAssets.reduce((total, asset) => total + asset.gzipBytes, 0)
const initialJavaScript = Buffer.concat(initialJavaScriptAssets.map((asset) => asset.content))
const maxInitialJavaScriptRawBytes = 260 * 1024
const maxInitialJavaScriptGzipBytes = 82 * 1024
if (initialJavaScriptRawBytes > maxInitialJavaScriptRawBytes || initialJavaScriptGzipBytes > maxInitialJavaScriptGzipBytes) {
  throw new Error(`public initial JavaScript exceeds budget: raw=${initialJavaScriptRawBytes}/${maxInitialJavaScriptRawBytes}, gzip=${initialJavaScriptGzipBytes}/${maxInitialJavaScriptGzipBytes}`)
}

const assetsDir = path.join(distDir, 'assets')
const assets = await readdir(assetsDir)
const requireChunk = (prefix) => {
  const matches = assets.filter((name) => name.startsWith(`${prefix}-`) && name.endsWith('.js'))
  if (matches.length !== 1) throw new Error(`expected one ${prefix} chunk, found ${matches.length}`)
  return matches[0]
}

const chunks = {
  admin: requireChunk('AdminDashboard'),
  nodeDetail: requireChunk('NodeDetailRoute'),
}
for (const [sentinel, owner] of [
  ['账号只能使用 3-64 位', 'account'],
  ['主题与背景', 'settings'],
]) {
  if (initialJavaScript.includes(sentinel)) {
    throw new Error(`${owner} module leaked into the public initial JavaScript graph`)
  }
}
for (const sentinel of ['同步监控服务…']) {
  if (initialJavaScript.includes(sentinel)) throw new Error(`detail module leaked into the public initial JavaScript graph: ${sentinel}`)
}

const flagDir = path.join(distDir, 'assets', 'flags')
const flags = (await readdir(flagDir)).filter((name) => name.endsWith('.svg'))
if (flags.length === 0 || flags.length > 300) {
  throw new Error(`unexpected 4:3 flag asset count: ${flags.length}`)
}
const distFiles = await readdir(assetsDir)
let chunkBytes = 0
for (const name of distFiles.filter((value) => value.endsWith('.js'))) {
  chunkBytes += (await stat(path.join(assetsDir, name))).size
}
const maxJavaScriptBytes = 420 * 1024
if (chunkBytes > maxJavaScriptBytes) {
  throw new Error(`total production JavaScript exceeds budget: ${chunkBytes}/${maxJavaScriptBytes}`)
}

console.log(JSON.stringify({
  entry: path.basename(entryPath),
  entryRawBytes,
  entryGzipBytes,
  initialJavaScriptFiles: initialJavaScriptAssets.map((asset) => asset.file),
  initialJavaScriptRawBytes,
  initialJavaScriptGzipBytes,
  chunks,
  javascriptBytes: chunkBytes,
  flagAssets: flags.length,
}, null, 2))
