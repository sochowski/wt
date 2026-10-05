import { defineTool } from '@earendil-works/pi-durable';
import { Type } from '@earendil-works/pi-ai';
import registerViews from '../../config/pi-wt/views.js';
import { deckParameters } from '../../config/pi-wt/presentation.js';
import { delegationTools } from './jobs.mjs';

/** Shared schemas/transport only: ordinary extension lifecycle/native provider are never loaded. */
export function wtTools(launch, fence, command) {
  const tools = [];
  registerViews({ registerTool(tool) {
    tools.push(defineTool({ name: tool.name, description: tool.description, parameters: tool.parameters, replay: 'unsafe', execute: async (p, api) => {
      await fence(); return tool.execute(api.callId, p);
    } }));
  } }, command, launch.root);
  tools.push(defineTool({ name: 'wt_agent', description: 'Independent named durable WT peers, never workflow delegation. Explicitly assign an attached checkout target. Their lifetimes are independent; stopping parent does not stop them. Writer leases prevent simultaneous durable writers to the same canonical cwd. Requests use root wake permits; notify does not wake. No focus changes. Workflow delegation uses the separate wt_delegate contract with mandatory review, not named peer creation.', parameters: Type.Object({
    action: Type.Union(['list','create','read','message','stop'].map(v=>Type.Literal(v))),
    name: Type.Optional(Type.String()), agent: Type.Optional(Type.String()), target: Type.Optional(Type.String()), body: Type.Optional(Type.String()), notify: Type.Optional(Type.Boolean()),
  }), replay:'unsafe', execute: async (p, api) => {
    await fence(); let args;
    switch(p.action) {
      case 'list': args=['agents','list',launch.root];break;
      case 'create':
        if (!p.name || !p.target) throw new Error('Name and explicit assignment target required');
        args=['agents','create',launch.root,p.name,'--parent',launch.agent,'--cwd',p.target,'--backend','durable'];break;
      case 'read': case 'stop':
        if(!p.agent)throw new Error('Agent required'); args=['agents',p.action,launch.root,p.agent];break;
      case 'message':
        if(!p.agent || !p.body)throw new Error('Recipient/body required');
        args=['message','send',launch.root,launch.agent,p.agent,p.body,'--id',`durable-tool:${launch.identity.uuid}:${api.callId}`];if(p.notify)args.push('--notify');break;
      default:throw new Error('Unsupported peer action');
    }
    const result=await command(args);
    return {content:[{type:'text',text:(typeof result==='string'?result:JSON.stringify(result)).slice(0,32768)}]};
  } }));
  tools.push(defineTool({ name:'wt_present_deck', description:'Show a managed WT presentation deck in the assigned checkout. Does not steal chat focus. Existing ownership/pin/human-active protections apply. Human can navigate in Neovim; this tool only reports that the deck was shown, NOT that it was reviewed or accepted.', parameters:deckParameters, replay:'unsafe', execute:async p=>{
    await fence();
    const workspace=await command(['workspace',launch.root]);
    const matches=workspace.checkouts?.filter(c=>c.path===launch.identity.cwd) || [];
    const target=workspace.cwd===launch.identity.cwd ? 'root' : matches.length===1 ? matches[0].id : undefined;
    if (!target) throw new Error('Assigned presentation target is no longer attached');
    const result=await command(['present',launch.root,target],{version:1,title:p.title,startIndex:p.startIndex || 1,scenes:p.scenes});
    return {content:[{type:'text',text:JSON.stringify(result).slice(0,32768)}]};
  } }));
  return [...tools, ...delegationTools(launch, fence, command)];
}
