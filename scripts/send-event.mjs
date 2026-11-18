#!/usr/bin/env node
import {createHmac,randomUUID} from 'node:crypto';
import {readFileSync,writeFileSync} from 'node:fs';
const path=process.argv[2];
const event=path?JSON.parse(readFileSync(path,'utf8')):{id:'evt_'+randomUUID(),type:'order.upsert',order:{external_id:'CLI-'+randomUUID().slice(0,8),version:1,customer:'CLI sample buyer',email:'buyer@example.test',currency:'USD',amount_minor:2500,shipping_minor:0,tax_minor:0,discount_minor:0,items:[{sku:'CLI-1',name:'Test item',quantity:1,unit_amount_minor:2500}]}};
const body=JSON.stringify(event),timestamp=Math.floor(Date.now()/1000);
const secret=process.env.DEMO_WEBHOOK_SECRET||'relaydesk-demo-signing-secret';
const signature=createHmac('sha256',secret).update(`${timestamp}.${body}`).digest('hex');
const res=await fetch((process.env.APP_URL||'http://127.0.0.1:8090')+'/webhooks/demo',{method:'POST',headers:{'Content-Type':'application/json','Relay-Signature':`t=${timestamp},v1=${signature}`},body});
console.log('HTTP',res.status,await res.text());
if(!res.ok)process.exitCode=1;
else if(!path){const file='sample-event.json';writeFileSync(file,JSON.stringify(event,null,2)+'\n');console.log(`Saved ${file}. Replay with: node scripts/send-event.mjs ${file}`);}
