'use strict';
const ids=['scene','theme','grey','dim','coarse'];
function render(){
 const query=new URLSearchParams({scene:document.getElementById('scene').value,theme:document.getElementById('theme').value,grey:document.getElementById('grey').checked?'1':'0',dim:document.getElementById('dim').checked?'1':'0',coarse:document.getElementById('coarse').checked?'1':'0'});
 document.getElementById('frames').innerHTML=[[1024,768],[1280,800],[1440,900],[1920,1080]].map(([width,height])=>`<section class="frame"><h2>${width} × ${height} <a href="screen.html?${query}" target="_blank" rel="noopener">Open screen ↗</a></h2><iframe title="Workspace ${width} by ${height}" width="${width}" height="${height}" src="screen.html?${query}"></iframe></section>`).join('');
}
ids.forEach(id=>document.getElementById(id).addEventListener('change',render));
document.querySelector('form').addEventListener('submit',ev=>ev.preventDefault());
render();
