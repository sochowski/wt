// WT native-compat-v2: only the three public SDK exports used by the pinned rpiv packages.
// Rendering semantics adapted from published Pi 0.87.1 (MIT); no SDK/runtime/private imports.
import hljs from 'highlight.js/lib/core.js';
import { createRequire } from 'node:module';
import { markdownTheme } from './presentation.mjs';
const require = createRequire(import.meta.url);
for (const name of ['python','java','go','javascript','cpp','typescript','php','ruby','c','csharp','nix','bash','rust','scala','kotlin','swift','dart','groovy','perl','lua']) hljs.registerLanguage(name, require(`highlight.js/lib/languages/${name}.js`));
let currentTheme;
export function bindPluginTheme(theme) { currentTheme = theme; }
export class SettingsManager {
  static create() { throw new Error('WT native-compat-v2 does not support external-editor settings/execution'); }
}
export class DynamicBorder {
  constructor(color = text => currentTheme.fg('border', text)) { this.color = color; }
  invalidate() {}
  render(width) { return [this.color('─'.repeat(Math.max(1, width)))]; }
}
const colors = { keyword:'syntaxKeyword',built_in:'syntaxType',literal:'syntaxNumber',number:'syntaxNumber',regexp:'syntaxString',string:'syntaxString',comment:'syntaxComment',doctag:'syntaxComment',meta:'muted',function:'syntaxFunction',title:'syntaxFunction',class:'syntaxType',type:'syntaxType',tag:'syntaxPunctuation',name:'syntaxKeyword',attr:'syntaxVariable',variable:'syntaxVariable',params:'syntaxVariable',operator:'syntaxOperator',punctuation:'syntaxPunctuation',addition:'toolDiffAdded',deletion:'toolDiffRemoved' };
function highlightedHtml(html, theme) {
  const scopes = [], formatters = Object.fromEntries(Object.entries(colors).map(([scope,color]) => [scope, text => theme.fg(color,text)]));
  Object.assign(formatters,{emphasis:text=>theme.italic(text),strong:text=>theme.bold(text),link:text=>theme.underline(text)});
  const format = text => {
    for (let i=scopes.length-1;i>=0;i--) {
      const scope=scopes[i];if(!scope)continue;
      const f=formatters[scope] || formatters[scope.split('.')[0]] || formatters[scope.split('-')[0]];
      if(f)return f(text);
    }
    return text;
  };
  const entities={amp:'&',lt:'<',gt:'>',quot:'"',apos:"'"};
  const decode = text => text.replace(/&([^;]{1,15});/g,(full,entity)=>{
    if(Object.hasOwn(entities,entity))return entities[entity];
    const n=/^#x/i.test(entity)?parseInt(entity.slice(2),16):entity.startsWith('#')?parseInt(entity.slice(1),10):NaN;
    return Number.isInteger(n)&&n>=0&&n<=0x10ffff?String.fromCodePoint(n):full;
  });
  return html.split(/(<span(?=[>\s])[^>]*>|<\/span>)/).map(part=>{
    if(part==='</span>'){scopes.pop();return '';}
    if(part.startsWith('<span')){const names=part.match(/\sclass\s*=\s*(?:"([^"]*)"|'([^']*)')/);scopes.push((names?.[1]||names?.[2]||'').split(/\s+/).find(s=>s.startsWith('hljs-'))?.slice(5));return '';}
    return part ? format(decode(part)) : '';
  }).join('');
}
export function getMarkdownTheme() {
  if(!currentTheme)throw new Error('WT plugin theme is not attached');
  const theme=currentTheme;
  return {...markdownTheme(theme),highlightCode:(code,lang)=>{
    if(!lang||!hljs.getLanguage(lang))return code.split('\n').map(line=>theme.fg('mdCodeBlock',line));
    try{return highlightedHtml(hljs.highlight(code,{language:lang,ignoreIllegals:true}).value,theme).split('\n');}
    catch{return code.split('\n').map(line=>theme.fg('mdCodeBlock',line));}
  }};
}
