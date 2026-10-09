import { TuiMainScreen, ProcessTerminal, Container, Text, CombinedAutocompleteProvider, getKeybindings, setKeybindings } from '@earendil-works/pi-tui';
import { BridgeDoc, context, stateCommand, humanSelfStop } from './runtime.mjs';
import { inboxPump } from './inbox.mjs';
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { jobPump, JobDoc } from './jobs.mjs';
import { appActions, loadPresentation, selectTheme } from './presentation.mjs';
import { renderConversation, footerText, plain } from './conversation-ui.mjs';
import { getSupportedThinkingLevels } from '@earendil-works/pi-ai/models';
import { ActionEditor, SearchSelector } from './editor-ui.mjs';
export { renderConversation } from './conversation-ui.mjs';

/** WT-owned public pi-tui controller; no native inference/session engine. */
export async function interactive(runtime, { terminal, poll = true, commandTransport = stateCommand, selfStop = humanSelfStop, stateObserver, presentation } = {}) {
  const previousKeybindings = getKeybindings();
  let tui, watch, pump, jobs, timer, signalClose, selectorContainer, selector, closePromise, closed = false;
  let observe = async () => {};
  const done = Promise.withResolvers();
  // Initialization may fail before the main wait is installed.
  void done.promise.catch(() => {});
  function close() {
    if (!closePromise) {
      closed = true;
      closePromise = (async () => {
        clearInterval(timer);
        const errors = [];
        const attempt = async operation => { try { await operation(); } catch (error) { errors.push(error); } };
        await attempt(() => pump?.stop()); await attempt(() => jobs?.stop());
        await attempt(() => observe('closing'));
        selector = undefined; await attempt(() => selectorContainer?.clear());
        await attempt(() => runtime.plugins?.detach());
        await attempt(() => watch?.stop()); await attempt(() => tui?.stop());
        await attempt(() => runtime.close());
        await attempt(() => stateObserver?.({ stage: 'closed', at: Date.now() }));
        if (errors.length) throw new AggregateError(errors, `Durable UI cleanup failed: ${errors[0].message}`);
      })();
      void closePromise.then(done.resolve, done.reject);
    }
    return closePromise;
  }
  try {
    terminal ??= new ProcessTerminal();
    presentation ??= loadPresentation(process.env.PI_CODING_AGENT_DIR || join(homedir(), '.pi', 'agent'));
    tui = new TuiMainScreen(terminal, presentation.showHardwareCursor);
    setKeybindings(presentation.keybindings);
    const { theme, keybindings } = presentation;
    const transcript = new Container(), notices = new Text('', 0, 0), status = new Text('', 0, 0);
    const editor = new ActionEditor(tui, { borderColor: text => theme.fg('borderAccent', text), selectList: selectTheme(theme) }, keybindings, { paddingX: presentation.editorPaddingX, autocompleteMaxVisible: presentation.autocompleteMaxVisible });
    selectorContainer = new Container();
    const widgets = new Container();
    tui.addChild(transcript); tui.addChild(notices); tui.addChild(selectorContainer); tui.addChild(widgets); tui.addChild(editor); tui.addChild(status); tui.setFocus(editor);
    const commands = ['help', 'hotkeys', 'quit', 'abort', 'stop', 'model', 'thinking', 'steer', 'followup', 'notifications', ...runtime.resources.skills.map(skill => `skill:${skill.name}`)];
    editor.setAutocompleteProvider(new CombinedAutocompleteProvider(commands.map(name => ({ name })), runtime.launch.identity.cwd, null));
    watch = await runtime.root.watch(context);
    for (const entry of watch.value.entries) for (const message of entry.model || []) if (message.role === 'user') {
      const text = typeof message.content === 'string' ? message.content : message.content?.filter(block => block.type === 'text').map(block => block.text).join('\n');
      if (text) editor.addToHistory(text);
    }
    let current = watch.value, queue = Promise.resolve(), reportedStatus, lastSubmission;
    let hideThinking = presentation.hideThinking, expandedTools = false, lastClear = -Infinity;
    if (runtime.plugins) await runtime.plugins.attach({ tui, theme, keybindings, editor, notices, widgets, getToolsExpanded: () => expandedTools });
    const notice = text => { notices.setText(theme.fg('warning', text)); tui.requestRender(); };
    function enqueue(operation) { queue = queue.then(() => closed ? undefined : operation()).catch(() => notice('Operation rejected or failed; no native fallback. Check command, runtime identity or authentication.')); }
    const models = () => [...runtime.models.getModels()].sort((a, b) => `${a.provider}/${a.id}`.localeCompare(`${b.provider}/${b.id}`));
    const thinkingLevels = agent => getSupportedThinkingLevels(runtime.models.getModel(agent.model.provider, agent.model.modelId));
    async function configure(config) { await runtime.fence(); if(runtime.plugins?.recoveryHeld||runtime.continuationHeld)throw new Error('Interrupted recovery cannot change configuration'); await runtime.root.configure(config, context); await update(current); }
    function select(title, items, selected, accept) {
      if (selector) return;
      selector = new SearchSelector(title, items, selected, theme, keybindings, value => {
        selector = undefined; selectorContainer.clear(); tui.setFocus(editor); tui.requestRender();
        if (value !== undefined) enqueue(() => accept(value));
      });
      selectorContainer.addChild(selector); tui.setFocus(selector); tui.requestRender();
    }
    async function selectModel() {
      const agent = await runtime.root.agent(context);
      select('Model (pinned catalog; selection does not change native settings)', models().map(model => ({ value: `${model.provider}/${model.id}`, label: `${model.provider}/${model.id}`, description: model.name || '' })), `${agent.model.provider}/${agent.model.modelId}`, value => {
        const slash = value.indexOf('/'); return configure({ model: { provider: value.slice(0, slash), modelId: value.slice(slash + 1) } });
      });
    }
    async function cycleModel(direction) {
      const agent = await runtime.root.agent(context), available = models();
      if (!available.length) throw new Error('No models');
      const index = available.findIndex(model => model.provider === agent.model.provider && model.id === agent.model.modelId);
      const model = available[(index + direction + available.length) % available.length]; await configure({ model: { provider: model.provider, modelId: model.id } });
    }
    observe = async (stage, value = current) => {
      if (!stateObserver) return;
      const live = value.docs['pi.live'];
      const submission = lastSubmission ? await (await runtime.harness.submission(lastSubmission, context)).status(context) : undefined;
      const inspection = await runtime.harness.inspect(context);
      await stateObserver({ stage, at: Date.now(), submission: submission ? { id: submission.id, status: submission.status, reason: submission.reason } : null, live: { run: Boolean(live?.run), generation: Boolean(live?.generation), tools: (live?.tools || []).length }, tasks: inspection.tasks.map(t => ({ id: t.record.id, state: t.state.kind })) });
    };
    async function input(...args) {
      const submission = await runtime.input(...args); lastSubmission = submission.id;
      await observe('admitted'); return submission;
    }
    pump = inboxPump(runtime, commandTransport); jobs = jobPump(runtime, commandTransport);
    async function update(value) {
      current = value;
      const nextStatus = value.docs['pi.live']?.run ? 'working' : 'idle';
      if (reportedStatus !== nextStatus) { await runtime.reportStatus(nextStatus); reportedStatus = nextStatus; }
      transcript.clear();
      for (const component of renderConversation(value, presentation, { hideThinking, expandedTools })) transcript.addChild(component);
      const agent = await runtime.root.agent(context);
      editor.borderColor = text => theme.fg(`thinking${agent.thinkingLevel === 'off' ? 'Off' : agent.thinkingLevel[0].toUpperCase() + agent.thinkingLevel.slice(1)}`, text);
      status.setText(footerText(value, agent, runtime.launch.identity.cwd, theme));
      const doc = await runtime.harness.snapshot(BridgeDoc, runtime.root.id, context);
      const jobDoc = await runtime.harness.snapshot(JobDoc, runtime.root.id, context);
      const uncertain = runtime.plugins ? await runtime.plugins.update() : [];
      notices.setText([...presentation.diagnostics, ...(runtime.plugins?.notice ? [runtime.plugins.notice] : []), ...(runtime.continuationHeld ? [`Interrupted v2 run ${runtime.interruptedRun.taskId} (${runtime.interruptedRun.kind} v${runtime.interruptedRun.version}) awaits explicit human-authorized continuation (unavailable at this checkpoint). Receipted tools remain completed; provider continuation is NOT exactly-once. No automatic input/model/inbox/job wake.`] : []), ...(uncertain.length ? [`Plugin recovery paused: ${uncertain.length} interrupted/uncertain operation(s). Stored answers/candidates are NOT completed tool receipts. Plugin execution/UI and new input are disabled; /plugin-inspect is read-only.`] : []), ...Object.values(doc?.notifications || {}).map(p => `Notification from ${plain(p.sender)}: ${plain(p.body)}`), ...Object.entries(jobDoc?.results || {}).map(([id, result]) => `Delegation ${id.slice(0,12)}: ${result.state}${result.state === 'succeeded' ? ' (independent review accepted)' : ' (not success until independent review)'}`)].join('\n').slice(-8192));
      tui.requestRender();
      await observe('view', value);
    }
    async function command(text) {
      const [name, ...parts] = text.trim().split(/\s+/), argument = parts.join(' ');
      if (name === '/quit') return close(); // close is not abort or stop.
      if (name === '/abort') { await runtime.abort(); return; }
      if (name === '/stop') { await runtime.fence(); await selfStop(runtime.launch); return close(); }
      if (name === '/hotkeys') { notice(Object.keys(appActions).map(action => `${action}: ${keybindings.getKeys(action).join(', ') || '(disabled)'}`).join('\n')); return; }
      if (name === '/help') {
        notices.setText('Enter: chat / busy steer; Alt-Enter followUp; Escape durably aborts; Ctrl-C clears, twice within 500ms recoverably closes. /hotkeys; /steer TEXT; /followup TEXT; /abort; /quit; /stop (private human WT stop); /model [PROVIDER/MODEL]; /thinking [LEVEL]; /skill:NAME [TEXT]; /notifications. Profile: wt-durable-v1; Harness/SQLite owns inference and state. Native-style UI is NOT native plugin/delegation parity. Supported resources: instruction files and nonexecuting skills (project skills require explicit per-launch trust). Native prompt overrides, project settings and extension/package scripts are not loaded. Questionnaire/todo, web/MCP and custom subagent/council plugins are not shipped. /model and /thinking change only this conversation. See docs/pi-durable-capabilities.md for the capability matrix.'); tui.requestRender(); return;
      }
      if (name === '/model') {
        if (!argument) return selectModel();
        const slash = argument.indexOf('/'), provider = argument.slice(0, slash), modelId = argument.slice(slash + 1);
        if (slash < 1 || !runtime.models.getModel(provider, modelId)) throw new Error('Unknown model');
        await configure({ model: { provider, modelId } }); return;
      }
      if (name === '/thinking') {
        if (!argument) { const agent = await runtime.root.agent(context); select('Thinking level (durable conversation only)', thinkingLevels(agent).map(value => ({ value, label: value })), agent.thinkingLevel, value => configure({ thinkingLevel: value })); return; }
        if (!['off','minimal','low','medium','high','xhigh','max'].includes(argument)) throw new Error('Unknown thinking level');
        await configure({ thinkingLevel: argument }); return;
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
      if (name.startsWith('/') && await runtime.plugins?.command(name.slice(1), argument)) return;
      if (name.startsWith('/')) throw new Error('Unsupported durable command');
      await input(text, current.docs['pi.live']?.run ? 'steer' : 'followUp');
    }
    function submit(text, mode) {
      if (!text.trim() || closed) return;
      editor.addToHistory(text); editor.setText('');
      enqueue(() => mode && !text.trimStart().startsWith('/') ? input(text, mode) : command(text));
    }
    editor.onSubmit = text => submit(text, current.docs['pi.live']?.run ? 'steer' : 'followUp');
    editor.onAction('app.clear', () => {
      const now = Date.now();
      if (now - lastClear < 500) enqueue(close);
      else { editor.setText(''); lastClear = now; notice('Editor cleared; press clear again within 500ms to close recoverably.'); }
    });
    editor.onAction('app.exit', () => enqueue(close));
    editor.onAction('app.interrupt', () => enqueue(() => runtime.abort()));
    editor.onAction('app.message.followUp', () => submit(editor.getExpandedText(), 'followUp'));
    editor.onAction('app.model.select', () => enqueue(selectModel));
    editor.onAction('app.model.cycleForward', () => enqueue(() => cycleModel(1)));
    editor.onAction('app.model.cycleBackward', () => enqueue(() => cycleModel(-1)));
    editor.onAction('app.thinking.cycle', () => enqueue(async () => { const agent = await runtime.root.agent(context); const levels = thinkingLevels(agent); await configure({ thinkingLevel: levels[(levels.indexOf(agent.thinkingLevel) + 1) % levels.length] }); }));
    editor.onAction('app.thinking.toggle', () => { hideThinking = !hideThinking; enqueue(() => update(current)); });
    editor.onAction('app.tools.expand', () => { expandedTools = !expandedTools; enqueue(() => update(current)); });
    for (const action of presentation.unsupportedActions) editor.onAction(action, () => notice(`Durable action not implemented: ${action}. No native fallback or external execution.`));
    signalClose = () => { void close().catch(done.reject); };
    process.on('SIGTERM', signalClose); process.on('SIGHUP', signalClose);
    await watch.start(update); await update(current); tui.start();
    await runtime.resume(); // Only after identity/fencing and initial retained render.
    if (poll) {
      let running = false;
      timer = setInterval(async () => {
        if (running || closed) return; running = true;
        try { await runtime.fence(); if(!runtime.plugins?.recoveryHeld&&!runtime.continuationHeld){await pump.poll(); await jobs.poll();} await update(current); }
        catch { notices.setText('WT fence/inbox unavailable: closing recoverably; no further automatic admission.'); tui.requestRender(); await close().catch(done.reject); }
        finally { running = false; }
      }, 1000);
    }
    await done.promise;
  } finally {
    if (signalClose) { process.off('SIGTERM', signalClose); process.off('SIGHUP', signalClose); }
    try { await close(); }
    finally { setKeybindings(previousKeybindings); }
  }
}
