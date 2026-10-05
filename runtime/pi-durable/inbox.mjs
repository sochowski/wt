import { admitMessage, stateCommand } from './runtime.mjs';

/** Bounded pages and single serial poll; ack is admission, not answer/effects. */
export function inboxPump(runtime, command = stateCommand) {
  let cursor = 0, busy = false, stopped = false;
  return {
    async poll() {
      if (busy || stopped) return;
      busy = true;
      try {
        await runtime.fence();
        const { root, agent } = runtime.launch;
        const page = await command(['message', 'poll', root, agent, '--after', String(cursor)]);
        for (const message of page.messages) {
          if (stopped) break;
          // Reservations remain eligible even after crashes/disabled new wakes.
          try { await command(['message', 'durable-claim', root, message.id]); }
          catch { continue; } // Unadmitted remains visible; UI reports backlog separately.
          await admitMessage(runtime, message);
          await command(['message', 'ack', root, message.id]);
        }
        cursor = page.messages.length ? page.next : 0;
      } finally { busy = false; }
    },
    stop() { stopped = true; },
  };
}
