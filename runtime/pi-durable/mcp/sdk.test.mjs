import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import * as sdk from './sdk.mjs';
const golden = JSON.parse(await readFile(new URL('./public-output-golden.json',import.meta.url),'utf8'));
test('narrow output bridge matches actual public SDK 1.0.4 golden without a native engine',()=>{
  assert.equal(golden.sdk,'@earendil-works/pi-coding-agent@1.0.4');
  assert.deepEqual(Object.keys(sdk).sort(),['DEFAULT_MAX_BYTES','DEFAULT_MAX_LINES','formatSize','truncateHead'].sort());
  assert.deepEqual({maxBytes:sdk.DEFAULT_MAX_BYTES,maxLines:sdk.DEFAULT_MAX_LINES},golden.defaults);
  for(const value of golden.sizes)assert.equal(sdk.formatSize(value.bytes),value.formatted);
  for(const value of golden.cases){
    const text=value.input.repeat?value.input.repeat.repeat(value.input.count):value.input.content;
    const {content,...metadata}=sdk.truncateHead(text,value.input.options);
    assert.deepEqual(metadata,value.metadata);
    assert.equal(createHash('sha256').update(content).digest('hex'),value.contentSha256);
  }
});
