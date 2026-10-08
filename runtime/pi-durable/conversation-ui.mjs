import { Box, Text, Markdown } from '@earendil-works/pi-tui';
import { markdownTheme } from './presentation.mjs';

// Never pass model-supplied terminal control sequences through themed components.
export const plain = value => String(value ?? '').replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f]/g, '');
const contentText = content => typeof content === 'string' ? content : (content || []).map(block => block.type === 'text' ? block.text : block.type === 'image' ? '[image: inline display not yet supported]' : '').filter(Boolean).join('\n');

function toolComponent(name, args, result, state, presentation, expanded, partial = false) {
  const { theme, outputPad } = presentation;
  const box = new Box(outputPad, 0, text => theme.bg(result?.isError ? 'toolErrorBg' : result && !partial ? 'toolSuccessBg' : 'toolPendingBg', text));
  const detail = args?.path ?? args?.command ?? (args ? JSON.stringify(args) : '');
  box.addChild(new Text(theme.fg('toolTitle', theme.bold(`${plain(name)} ${plain(detail).slice(0, 1024)}`)), 0, 0));
  if (partial) box.addChild(new Text(theme.fg('dim', state), 0, 0));
  const output = plain(contentText(result?.content));
  if (output) {
    const lines = output.split('\n');
    box.addChild(new Text(theme.fg('toolOutput', (expanded ? lines : lines.slice(0, 5)).join('\n').slice(0, 32768)), 0, 0));
    if (!expanded && lines.length > 5) box.addChild(new Text(theme.fg('dim', `… ${lines.length - 5} more lines (expand tools)`), 0, 0));
  } else box.addChild(new Text(theme.fg('dim', state), 0, 0));
  return box;
}

/** Presentation of real durable model entries; no synthetic native session tree/IDs. */
export function renderConversation(value, presentation, { hideThinking = presentation.hideThinking, expandedTools = false } = {}) {
  const { theme, outputPad } = presentation, md = markdownTheme(theme), components = [];
  const messages = value.entries.flatMap(entry => entry.model || []);
  const live = value.docs['pi.live'];
  if (live?.generation?.message) messages.push(live.generation.message);
  const results = new Map(messages.filter(message => message.role === 'toolResult').map(message => [message.toolCallId, message]));
  const calls = new Set();
  for (const message of messages) {
    if (message.role === 'system') continue;
    if (message.role === 'user') {
      const box = new Box(outputPad, 1, text => theme.bg('userMessageBg', text));
      box.addChild(new Markdown(plain(contentText(message.content)), 0, 0, md, { color: text => theme.fg('userMessageText', text) }));
      components.push(box);
    } else if (message.role === 'assistant') {
      for (const block of message.content || []) {
        if (block.type === 'text') components.push(new Markdown(plain(block.text), outputPad, 0, md));
        else if (block.type === 'thinking') {
          if (hideThinking) components.push(new Text(theme.fg('dim', '[thinking hidden]'), outputPad, 0));
          else components.push(new Markdown(plain(block.thinking), outputPad, 0, md, { color: text => theme.fg('thinkingText', text), italic: true }));
        } else if (block.type === 'toolCall') {
          calls.add(block.id);
          const running = live?.tools?.find(tool => tool.callId === block.id);
          const result = results.get(block.id) || (running?.output ? { content: [{ type: 'text', text: String(running.output) }], isError: false } : undefined);
          components.push(toolComponent(block.name, block.arguments, result, running ? `[${running.status}: no completed result yet]` : '[pending/interrupted: no completed result]', presentation, expandedTools, !results.has(block.id)));
        }
      }
      if (message.stopReason === 'error') components.push(new Text(theme.fg('error', 'Provider error (details suppressed; check authentication in ordinary Pi).'), outputPad, 0));
      if (message.stopReason === 'aborted') components.push(new Text(theme.fg('warning', 'Interrupted'), outputPad, 0));
    } else if (message.role === 'toolResult' && !calls.has(message.toolCallId)) {
      components.push(toolComponent(message.toolName, undefined, message, '', presentation, expandedTools));
    } else if (message.role !== 'toolResult') components.push(new Markdown(plain(contentText(message.content)), outputPad, 0, md));
  }
  for (const tool of live?.tools || []) if (!calls.has(tool.callId)) components.push(toolComponent(tool.name, tool.arguments, tool.output ? { content: [{ type: 'text', text: String(tool.output) }] } : undefined, `[${tool.status}: no completed result yet]`, presentation, expandedTools, true));
  return components;
}

export function footerText(value, agent, cwd, theme) {
  const usage = value.entries.flatMap(entry => entry.model || []).filter(message => message.role === 'assistant').reduce((totals, message) => ({ input: totals.input + (message.usage?.input || 0), output: totals.output + (message.usage?.output || 0), cost: totals.cost + (message.usage?.cost?.total || 0) }), { input: 0, output: 0, cost: 0 });
  return `${theme.fg('dim', plain(cwd))}\n${theme.fg('muted', `↑${usage.input} ↓${usage.output} $${usage.cost.toFixed(4)} | ${agent.model.provider}/${agent.model.modelId} thinking=${agent.thinkingLevel} | ${value.docs['pi.live']?.run ? 'working' : 'idle'} | /help`)}`;
}
