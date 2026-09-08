// Display-only formatting: never replace message.content or receipt details.
export function inboxDisplayText(text) {
  return text.replace(/[\x00-\x09\x0b-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]/g,
    char => `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`)
}
function shortLine(text, limit = 160) {
  const safe = inboxDisplayText(text).replace(/\s+/g, " ").trim()
  return safe.length > limit ? safe.slice(0, limit - 1) + "…" : safe
}
export function formatInbox(message, expanded = false) {
  const content = typeof message.content === "string" ? message.content : ""
  if (expanded) return inboxDisplayText(content)
  const prefix = /^(Human|Peer [^\n]+):\n/.exec(content)
  const sender = prefix?.[1] || "Message"
  const body = prefix ? content.slice(prefix[0].length) : content
  let title = `WT inbox · ${shortLine(sender, 48)}`, prose = body
  try {
    const envelope = JSON.parse(body)
    if (envelope?.type === "wt-job-result" && typeof envelope.job === "string" && typeof envelope.output === "string") {
      title += ` · Job ${shortLine(envelope.job, 16)} result`
      prose = envelope.output
    }
  } catch { /* Ordinary text and malformed envelopes use the bounded preview. */ }
  // Reports remain available in full on expansion, not as raw JSON by default.
  prose = prose.split(/```acceptance-report\b/, 1)[0]
  const lines = prose.split("\n").map(line => line.trim()).filter(Boolean)
  const preview = lines.slice(0, 3).map(line => shortLine(line))
  if (!preview.length) preview.push("(details available on expand)")
  else if (lines.length > 3) preview[preview.length - 1] += " …"
  return [title, ...preview].join("\n")
}

// Inject Pi components to keep the formatter and Node tests native-import free.
export function createInboxRenderer({ Text, keyHint }) {
  return (message, { expanded, outputPad = 0 }, theme) => {
    const text = new Text(formatInbox(message, expanded), outputPad, 0)
    const hint = new Text("", outputPad, 0)
    return {
      render(width) {
        const lines = text.render(width)
        if (expanded) return lines
        hint.setText(theme.fg("dim", keyHint("app.tools.expand", "for full inbox message")))
        // Bound actual terminal rows too, including on narrow panes.
        return [...lines.slice(0, 5), ...hint.render(width).slice(0, 1)]
      },
      invalidate() { text.invalidate(); hint.invalidate() },
    }
  }
}
