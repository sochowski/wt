// Explicit whole-checkout v2 evidence: all tracked/untracked/ignored files,
// bounded streaming hashes, semantic index, compact publication. No sampling.
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { lstat,readlink,open } from 'node:fs/promises';
import { constants } from 'node:fs';
import { join,relative,isAbsolute } from 'node:path';
import { createHash } from 'node:crypto';
const exec=promisify(execFile);
export const EVIDENCE_V2='whole-checkout-v2';
export const EVIDENCE_LIMITS_V2={paths:65536,bytes:512*1024*1024,fileBytes:64*1024*1024,inventoryBytes:16*1024*1024,changes:64,diffBytes:32768};
const hash=value=>createHash('sha256').update(typeof value==='string'||Buffer.isBuffer(value)?value:JSON.stringify(value)).digest('hex');
const sha=value=>typeof value==='string'&&/^[a-f0-9]{64}$/.test(value);
export function evidenceSummary(snapshot){
 const files=hash(snapshot.files),index=hash(snapshot.index),diff=hash(snapshot.diff);
 const base={version:2,profile:EVIDENCE_V2,head:snapshot.head,filesDigest:files,indexDigest:index,diffDigest:diff,fileCount:Object.keys(snapshot.files).length,totalBytes:snapshot.totalBytes};
 return {...base,digest:hash(base)};
}
export async function checkoutEvidenceV2(cwd){
 const args=['--no-optional-locks','--no-pager','-c','core.fsmonitor=false','-c','core.hooksPath=/dev/null','-C',cwd];
 const options={timeout:30000,maxBuffer:EVIDENCE_LIMITS_V2.inventoryBytes,env:{...process.env,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:'/dev/null'}};
 const git=async more=>(await exec('git',[...args,...more],options)).stdout;
 const filters=await exec('git',[...args,'config','--get-regexp','^filter\\..*\\.(clean|process|smudge)$'],options).catch(error=>{if(error.code===1)return{stdout:''};throw error;});
 if(filters.stdout.trim())throw new Error('Executable repository filters are unsupported by v2 evidence');
 if((await git(['rev-parse','--show-toplevel'])).trim()!==cwd)throw new Error('V2 evidence requires exact canonical checkout root');
 const head=(await git(['rev-parse','HEAD'])).trim();
 const paths=[...new Set((await git(['ls-files','-z','--cached','--others'])).split('\0').filter(Boolean))].sort();
 if(paths.length>EVIDENCE_LIMITS_V2.paths)throw new Error('V2 evidence path limit exceeded');
 const files=Object.create(null);let totalBytes=0;
 for(const path of paths){
  const rel=relative(cwd,join(cwd,path));if(isAbsolute(path)||rel==='..'||rel.startsWith('../'))throw new Error('V2 evidence path escapes checkout');
  let info;try{info=await lstat(join(cwd,path));}catch(error){if(error.code==='ENOENT'){files[path]=null;continue;}throw error;}
  if(info.isSymbolicLink()){files[path]={kind:'link',hash:hash(await readlink(join(cwd,path)))};continue;}
  if(!info.isFile())throw new Error('Unsupported v2 evidence file type');
  totalBytes+=info.size;if(info.size>EVIDENCE_LIMITS_V2.fileBytes||totalBytes>EVIDENCE_LIMITS_V2.bytes)throw new Error('V2 evidence byte limit exceeded');
  const file=await open(join(cwd,path),constants.O_RDONLY|(constants.O_NOFOLLOW??0));
  try{
   const before=await file.stat();if(before.dev!==info.dev||before.ino!==info.ino||before.size!==info.size||before.mtimeMs!==info.mtimeMs)throw new Error('V2 evidence file changed while opening');
   const digest=createHash('sha256');let read=0;
   if(info.size<=256*1024){const data=await file.readFile();read=data.length;digest.update(data);}
   else for await(const chunk of file.createReadStream({autoClose:false,highWaterMark:65536})){read+=chunk.length;if(read>EVIDENCE_LIMITS_V2.fileBytes||read>info.size)throw new Error('V2 evidence file grew while hashing');digest.update(chunk);}
   const after=await file.stat();
   if(read!==info.size||after.size!==before.size||after.mtimeMs!==before.mtimeMs||after.ctimeMs!==before.ctimeMs)throw new Error('V2 evidence file changed while hashing');
   files[path]={kind:'file',mode:info.mode&0o777,hash:digest.digest('hex')};
  }finally{await file.close();}
 }
 const diff=await git(['diff','--no-ext-diff','--no-textconv','--binary','HEAD','--']);if(Buffer.byteLength(diff)>EVIDENCE_LIMITS_V2.diffBytes)throw new Error('V2 changed diff exceeds review bound');
 const indexPath=(await git(['rev-parse','--path-format=absolute','--git-path','index'])).trim();if(!(await lstat(indexPath)).isFile())throw new Error('V2 requires regular semantic Git index');
 const index=(await git(['ls-files','--stage','-v','-z'])).split('\0').filter(Boolean).sort();
 const snapshot={version:2,head,files,index,diff,totalBytes};return {...snapshot,summary:evidenceSummary(snapshot)};
}
export function compactEvidenceV2(before,after){
 const paths=[...new Set([...Object.keys(before.files),...Object.keys(after.files)])].filter(path=>JSON.stringify(before.files[path]??null)!==JSON.stringify(after.files[path]??null)).sort();
 if(paths.length>EVIDENCE_LIMITS_V2.changes)throw new Error('V2 changed artifact count exceeds full-review bound');
 return {version:2,profile:EVIDENCE_V2,before:evidenceSummary(before),after:evidenceSummary(after),changes:paths.map(path=>({path,before:before.files[path]??null,after:after.files[path]??null})),diff:after.diff};
}
export function validateCompactEvidenceV2(proof){
 if(proof.version!==2||proof.profile!==EVIDENCE_V2||!Array.isArray(proof.changes)||proof.changes.length>EVIDENCE_LIMITS_V2.changes||typeof proof.diff!=='string'||Buffer.byteLength(proof.diff)>EVIDENCE_LIMITS_V2.diffBytes)throw new Error('Invalid compact v2 evidence');
 for(const summary of [proof.before,proof.after]){
  if(!summary||summary.version!==2||summary.profile!==EVIDENCE_V2||!sha(summary.digest)||!sha(summary.filesDigest)||!sha(summary.indexDigest)||!sha(summary.diffDigest)||typeof summary.head!=='string'||!(/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/).test(summary.head)||!Number.isSafeInteger(summary.fileCount)||summary.fileCount<0||summary.fileCount>EVIDENCE_LIMITS_V2.paths||!Number.isSafeInteger(summary.totalBytes)||summary.totalBytes<0||summary.totalBytes>EVIDENCE_LIMITS_V2.bytes)throw new Error('Invalid v2 whole-checkout summary');
  const {digest,...base}=summary;if(hash(base)!==digest)throw new Error('V2 summary digest mismatch');
 }
 if(hash(proof.diff)!==proof.after.diffDigest)throw new Error('V2 retained diff digest mismatch');
 if((proof.changes.length===0)!==(proof.before.filesDigest===proof.after.filesDigest))throw new Error('V2 change manifest contradicts whole-checkout digests');
 const paths=proof.changes.map(change=>change.path);
 if(paths.some(path=>typeof path!=='string'||!path||isAbsolute(path)||path.split('/').includes('..'))||new Set(paths).size!==paths.length||JSON.stringify(paths)!==JSON.stringify([...paths].sort()))throw new Error('Invalid v2 changed path inventory');
 for(const change of proof.changes)if(JSON.stringify(change.before)===JSON.stringify(change.after))throw new Error('V2 change entry is unchanged');
 for(const change of proof.changes)for(const file of [change.before,change.after])if(file!==null&&(!file||!['file','link'].includes(file.kind)||!sha(file.hash)||file.kind==='file'&&(!Number.isInteger(file.mode)||file.mode<0||file.mode>0o777)))throw new Error('Invalid v2 artifact metadata');
 return paths;
}
