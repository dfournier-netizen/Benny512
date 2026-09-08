'use strict';
const fs=require('fs'),vm=require('vm'),path=require('path'),assert=require('assert');
let active=0,maxActive=0,calls=[],delay=null;
const timers=[];
const api={
 getSupportedParameters:async()=>({known:true,pids:[]}),
 getDeviceParams:async()=>[],
 getParam:async(uid,name,args)=>{
  if(name==='device_info')return {value:{CurrentPersonality:2,PersonalityCount:3}};
  if(name==='dmx_personality')return {value:{Current:2,Count:3}};
  if(name!=='dmx_personality_description')return {value:''};
  calls.push(uid+':'+args.index);active++;maxActive=Math.max(maxActive,active);
  await new Promise(resolve=>setImmediate(resolve));
  if(delay)await delay;
  active--;
  if(uid==='nack')throw Error('NACK');
  return {value:{Index:args.index,DMXFootprint:16,Description:'Named mode '+args.index}};
 }
};
const ctx=vm.createContext({Api:api,console,Date,Map,Set,Promise,window:{addEventListener(){}},document:{addEventListener(){},activeElement:null},Live:{send(){},on(){}},setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){}});
vm.runInContext(fs.readFileSync(path.join(__dirname,'..','devicedetail.js'),'utf8'),ctx);
const detail=vm.runInContext('DeviceDetail',ctx);
async function settle(){for(let i=0;i<30;i++)await new Promise(resolve=>setImmediate(resolve));}
(async()=>{
 detail.select('auto',{params:true});timers.shift()();await settle();
 assert.deepEqual(calls,['auto:2','auto:1','auto:3'],'opening Parameters fetches every mode name automatically, current first');
 assert.equal(maxActive,1,'description requests are serialized');
 assert.equal(detail._caches.paramsCache.auto.personality[3],'Named mode 3');
 detail.select('auto',{params:true});await settle();assert.equal(calls.length,3,'reopening does not reread names');
 const before=calls.length;await Promise.all([detail._modeNames.fetch('shared',1),detail._modeNames.fetch('shared',1)]);
 assert.equal(calls.length,before+1,'concurrent info/params requests share one GET');
 await assert.rejects(detail._modeNames.fetch('nack',1));const nackCount=calls.length;
 await assert.rejects(detail._modeNames.fetch('nack',1));assert.equal(calls.length,nackCount,'failed labels have retry backoff');
 assert.match(detail._modeNames.format({CurrentPersonality:1,PersonalityCount:3},null,false),/Mode name unavailable/);
 assert.match(detail._modeNames.format({CurrentPersonality:1,PersonalityCount:3},null,true),/Loading mode name/);
 let release;delay=new Promise(resolve=>release=resolve);
 detail.select('cancel',{params:true});timers.pop()();await settle();
 detail.deselect();release();delay=null;await settle();
 assert.deepEqual(calls.filter(c=>c.startsWith('cancel:')),['cancel:2'],'leaving a fixture stops the remaining name queue');
 console.log('automatic mode names, ordering, single-flight, caching, backoff, and cancellation passed');
})().catch(e=>{console.error(e);process.exitCode=1;});
