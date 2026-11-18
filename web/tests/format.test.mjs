import {test} from 'node:test';
import assert from 'node:assert/strict';
import {escapeHTML,money,label,validStatus,age} from '../dist/format.js';
test('untrusted customer and error content cannot inject markup',()=>{
 assert.equal(escapeHTML('<img src=x onerror="alert(1)">'), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;');
 assert.equal(escapeHTML("Morgan & 'Co'"),'Morgan &amp; &#39;Co&#39;');
});
test('amounts use the currency minor-unit precision',()=>{
 assert.equal(money(6800,'USD'),'$68.00');
 assert.equal(money(6800,'JPY'),'¥6,800');
 assert.match(money(6800,'KWD'),/6\.800/);
});
test('unknown currencies have a readable bounded fallback',()=>{
 assert.equal(money(50,'not-a-currency'),'50 not-a-currency (minor units)');
});
test('delivery state classes come from an allowlist',()=>{
 assert.equal(label('retry_wait'),'Retry scheduled');
 assert.equal(validStatus('failed'),'failed');
 assert.equal(validStatus('x" onclick="bad'),'pending');
});
test('timestamps handle absent, malformed and future values',()=>{
 assert.equal(age(null),'Not yet');assert.equal(age('invalid'),'Unknown');
 assert.equal(age(new Date(Date.now()+60_000).toISOString()),'0s ago');
});
