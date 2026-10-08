// Development-only loopback file server. No dependencies and no output connection.
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../../..');
const types={'.html':'text/html; charset=utf-8','.css':'text/css; charset=utf-8','.js':'text/javascript; charset=utf-8','.mjs':'text/javascript; charset=utf-8','.svg':'image/svg+xml','.json':'application/json','.md':'text/plain; charset=utf-8'};
http.createServer((req,res)=>{
 try{
  let pathname=decodeURIComponent(new URL(req.url,'http://localhost').pathname);
  if(pathname.endsWith('/'))pathname+='index.html';
  const file=path.resolve(root,'.'+pathname);
  const rel=path.relative(root,file);
  if(rel.startsWith('..')||path.isAbsolute(rel)||rel.split(path.sep).some(p=>p.startsWith('.'))){res.writeHead(403).end();return;}
  if(!types[path.extname(file)]){res.writeHead(403).end();return;}
  fs.readFile(file,(error,data)=>{if(error){res.writeHead(404).end('Not found');return;}res.writeHead(200,{'Content-Type':types[path.extname(file)],'Cache-Control':'no-store'}).end(data);});
 }catch{res.writeHead(400).end();}
}).listen(8765,'127.0.0.1',()=>console.log('Design review: http://127.0.0.1:8765/docs/design/console-lite/'));
