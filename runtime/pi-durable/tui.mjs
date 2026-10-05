import { TuiMainScreen, ProcessTerminal, Editor, Container, Text, Markdown, matchesKey } from '@earendil-works/pi-tui';
import { BridgeDoc, context, stateCommand, humanSelfStop } from './runtime.mjs';
import { inboxPump } from './inbox.mjs';
import { readFileSync } from 'node:fs';
import { jobPump, JobDoc } from './jobs.mjs';

const plain = s => String(s ?? '').replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f]/g, '');
const identity = s => s;
const markdownTheme = Object.fromEntries(['heading','link','linkUrl','code','codeBlock','codeBlockBorder','quote','quoteBorder','hr','listBullet','bold','italic','strikethrough','underline'].map(k => [k, identity]));
const editorTheme = { borderColor: identity, selectList: { selectedPrefix: identity, selectedText: identity, description: identity, scrollInfo: identity, noMatch: identity } };

export function renderConversation(value) {
  const components = [];
  function message(m) {
    if (m.role === 'system') return;
    components.push(new Text(`${m.role}${m.toolName ? ` ${m.toolName}` : ''}${m.isError ? ' ERROR' : ''}${m.stopReason === 'aborted' ? ' INTERRUPTED' : ''}`, 0, 0));
    const text = typeof m.content === 'string' ? m.content : m.content?.map(b => b.type === 'text' ? b.text : b.type === 'thinking' ? `[thinking] ${b.thinking}` : b.type === 'toolCall' ? `[tool ${b.name}] ${JSON.stringify(b.arguments)}` : '[image]').join('\n');
    components.push(new Markdown(plain(text).slice(-32768), 0, 0, markdownTheme));
    if (m.stopReason === 'error') components.push(new Text('Provider error (details suppressed; authentication may need configuration in ordinary Pi).', 0, 0));
  }
  for (const entry of value.entries.slice(-100)) for (const m of entry.model || []) message(m);
  const live = value.docs['pi.live'];
  if (live?.generation?.message) message(live.generation.message);
  for (const tool of live?.tools || []) components.push(new Text(plain(`[${tool.status}] ${tool.name}\n${tool.output || ''}`).slice(-8192), 0, 0));
  return components;
}

/** WT-owned public pi-tui controller. No native extension host is loaded. */
export async function interactive(runtime, { terminal = new ProcessTerminal(), poll = true, commandTransport = stateCommand, selfStop = humanSelfStop, stateObserver } = {}) {
  const tui = new TuiMainScreen(terminal, true);
  const transcript = new Container(), notices = new Text('', 0, 0), status = new Text('', 0, 0);
  const editor = new Editor(tui, editorTheme);
  tui.addChild(transcript); tui.addChild(notices); tui.addChild(status); tui.addChild(editor); tui.setFocus(editor);
  const watch = await runtime.root.watch(context);
  let current = watch.value, closed = false, queue = Promise.resolve(), timer, reportedStatus, lastSubmission;
  async function observe(stage, value = current) {
    if (!stateObserver) return;
    const live = value.docs['pi.live'];
    const submission = lastSubmission ? await (await runtime.harness.submission(lastSubmission, context)).status(context) : undefined;
    const inspection = await runtime.harness.inspect(context);
    await stateObserver({ stage, at: Date.now(), submission: submission ? { id: submission.id, status: submission.status, reason: submission.reason } : null, live: { run: Boolean(live?.run), generation: Boolean(live?.generation), tools: (live?.tools || []).length }, tasks: inspection.tasks.map(t => ({ id: t.record.id, state: t.state.kind })) });
  }
  async function input(...args) {
    const submission = await runtime.input(...args); lastSubmission = submission.id;
    await observe('admitted'); return submission;
  }
  const done = Promise.withResolvers();
  const pump = inboxPump(runtime, commandTransport), jobs = jobPump(runtime, commandTransport);
  async function update(value) {
    current = value;
    const nextStatus = value.docs['pi.live']?.run ? 'working' : 'idle';
    if (reportedStatus !== nextStatus) { await runtime.reportStatus(nextStatus); reportedStatus = nextStatus; }
    transcript.clear();
    for (const component of renderConversation(value)) transcript.addChild(component);
    const agent = await runtime.root.agent(context);
    status.setText(`${agent.model.provider}/${agent.model.modelId} thinking=${agent.thinkingLevel} | ${value.docs['pi.live']?.run ? 'working (Enter queues followUp)' : 'idle'} | /help`);
    const doc = await runtime.harness.snapshot(BridgeDoc, runtime.root.id, context);
    const jobDoc = await runtime.harness.snapshot(JobDoc, runtime.root.id, context);
    notices.setText([...Object.values(doc?.notifications || {}).map(p => `Notification from ${plain(p.sender)}: ${plain(p.body)}`), ...Object.entries(jobDoc?.results || {}).map(([id, result]) => `Delegation ${id.slice(0,12)}: ${result.state}${result.state === 'succeeded' ? ' (independent review accepted)' : ' (not success until independent review)'}`)].join('\n').slice(-8192));
    tui.requestRender();
    await observe('view', value);
  }
  async function close() {
    if (closed) return;
    closed = true; clearInterval(timer); pump.stop(); jobs.stop();
    await observe('closing');
    await watch.stop(); tui.stop();
    await runtime.close();
    await stateObserver?.({ stage: 'closed', at: Date.now() }); done.resolve();
  }
  async function command(text) {
    const [name, ...parts] = text.trim().split(/\s+/), argument = parts.join(' ');
    if (name === '/quit') return close(); // close is not abort or stop.
    if (name === '/abort') { await runtime.abort(); return; }
    if (name === '/stop') { await runtime.fence(); await selfStop(runtime.launch); return close(); }
    if (name === '/help') {
      notices.setText('Enter: chat / queued followUp; /steer TEXT; /followup TEXT; /abort (durably cancel); /quit (recoverable close); /stop (WT stop); /model [PROVIDER/MODEL]; /thinking LEVEL; /skill:NAME [TEXT]; /notifications (deliver stored notifications on an authorized user turn). Ctrl-C aborts, Ctrl-D closes when empty.'); tui.requestRender(); return;
    }
    if (name === '/model') {
      if (!argument) { notices.setText(runtime.models.getModels().map(m => `${m.provider}/${m.id}`).join('\n')); tui.requestRender(); return; }
      const slash = argument.indexOf('/'), provider = argument.slice(0, slash), modelId = argument.slice(slash + 1);
      if (slash < 1 || !runtime.models.getModel(provider, modelId)) throw new Error('Unknown model');
      await runtime.fence(); await runtime.root.configure({ model: { provider, modelId } }, context); await update(current); return;
    }
    if (name === '/thinking') {
      if (!['off','minimal','low','medium','high','xhigh','max'].includes(argument)) throw new Error('Unknown thinking level');
      await runtime.fence(); await runtime.root.configure({ thinkingLevel: argument }, context); await update(current); return;
    }
    if (name === '/notifications') {
      const doc = await runtime.harness.snapshot(BridgeDoc, runtime.root.id, context);
      const contents = Object.values(doc?.notifications || {});
      if (contents.length) await input(`WT informational notifications (not assignments):\n${contents.map(p => `${p.sender}: ${p.body}`).join('\n')}`);
      return;
    }
    if (name.startsWith('/skill:')) {
      const skill = runtime.resources.skills.find(s => s.name === name.slice(7));
      if (!skill) throw new Error('Unknown/unsupported skill');
      await input(`Skill from ${JSON.stringify(skill.filePath)}; relative paths resolve from its directory:\n${readFileSync(skill.filePath, 'utf8')}\n${argument}`); return;
    }
    if (name === '/steer' || name === '/followup') { if (!argument) throw new Error('Input required'); await input(argument, name === '/steer' ? 'steer' : 'followUp'); return; }
    if (name.startsWith('/')) throw new Error('Unsupported durable command');
    await input(text);
  }
  function submit(text) {
    if (!text.trim() || closed) return;
    editor.addToHistory(text); editor.setText('');
    queue = queue.then(() => command(text)).catch(() => { notices.setText('Operation rejected or failed; no native fallback. Check command, runtime identity or authentication.'); tui.requestRender(); });
  }
  editor.onSubmit = submit;
  tui.addInputListener(data => {
    if (matchesKey(data, 'ctrl+c')) { submit('/abort'); return { consume: true }; }
    if (matchesKey(data, 'ctrl+d') && !editor.getText()) { submit('/quit'); return { consume: true }; }
  });
  const signalClose = () => { void close().catch(done.reject); };
  process.on('SIGTERM', signalClose); process.on('SIGHUP', signalClose);
  try {
    watch.start(update); await update(current); tui.start();
    await runtime.resume(); // Only after identity/fencing and initial retained render.
    if (poll) {
      let running = false;
      timer = setInterval(async () => {
        if (running || closed) return; running = true;
        try { await runtime.fence(); await pump.poll(); await jobs.poll(); await update(current); }
        catch { notices.setText('WT fence/inbox unavailable: closing recoverably; no further automatic admission.'); tui.requestRender(); await close(); }
        finally { running = false; }
      }, 1000);
    }
    await done.promise;
  } finally {
    process.off('SIGTERM', signalClose); process.off('SIGHUP', signalClose);
    await close();
  }
}
