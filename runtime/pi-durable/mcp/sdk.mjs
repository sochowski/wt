// Narrow public output-export semantics used by pi-mcp-adapter@5.1.0.
// Compatible with published Pi 1.0.4 public exports (MIT), golden-tested below.
// No native SDK, private imports, session engine, settings, or inference APIs.
export const DEFAULT_MAX_BYTES = 50 * 1024;
export const DEFAULT_MAX_LINES = 2000;
export function formatSize(bytes) {
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}
export function truncateHead(content, options = {}) {
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
  const lines = content.length ? content.split('\n') : [];
  if (content.endsWith('\n')) lines.pop();
  const totalBytes = Buffer.byteLength(content, 'utf8'), totalLines = lines.length;
  const metadata = { totalLines, totalBytes, lastLinePartial: false, firstLineExceedsLimit: false, maxLines, maxBytes };
  if (totalBytes <= maxBytes && totalLines <= maxLines) return {
    content, truncated: false, truncatedBy: null, ...metadata, outputLines: totalLines, outputBytes: totalBytes,
  };
  if (Buffer.byteLength(lines[0], 'utf8') > maxBytes) return {
    content: '', truncated: true, truncatedBy: 'bytes', ...metadata, firstLineExceedsLimit: true, outputLines: 0, outputBytes: 0,
  };
  const kept = []; let bytes = 0, truncatedBy = 'lines';
  for (let i = 0; i < lines.length && i < maxLines; i++) {
    const size = Buffer.byteLength(lines[i], 'utf8') + (i ? 1 : 0);
    if (bytes + size > maxBytes) { truncatedBy = 'bytes'; break; }
    kept.push(lines[i]); bytes += size;
  }
  if (kept.length >= maxLines && bytes <= maxBytes) truncatedBy = 'lines';
  const result = kept.join('\n');
  return { content: result, truncated: true, truncatedBy, ...metadata, outputLines: kept.length, outputBytes: Buffer.byteLength(result, 'utf8') };
}
