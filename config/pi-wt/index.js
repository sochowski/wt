import registerExtension from "./extension.js"
import renderInbox from "./inbox-renderer.js"

// Static imports let Pi's extension loader resolve its TUI packages. Keep the
// factory injectable so ordinary Node tests do not load native Pi dependencies.
export default function WtExtension(pi) {
  return registerExtension(pi, { renderInbox })
}
