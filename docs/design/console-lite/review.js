'use strict';
const viewports = [[390,844,'Phone'],[1024,768,'Tablet landscape'],[820,1180,'Tablet portrait'],[1440,900,'Desktop']];
const controls = ['scene','theme','grey','dim'].map(id=>document.getElementById(id));
function renderReview() {
 const [scene,theme,grey,dim]=controls;
 document.documentElement.dataset.theme=theme.value;
 document.getElementById('frames').innerHTML=viewports.map(([w,h,name])=>{
  const query=new URLSearchParams({scene:scene.value,theme:theme.value,grey:grey.checked?'1':'0',dim:dim.checked?'1':'0'});
  return `<section class="viewport"><h2>${name} <span>${w} × ${h}</span> <a href="screen.html?${query}" target="_blank" rel="noopener">Open screen ↗</a></h2><div class="frame-scroll"><iframe title="${name}: ${scene.selectedOptions[0].text}" src="screen.html?${query}" width="${w}" height="${h}"></iframe></div></section>`;
 }).join('');
}
controls.forEach(el=>el.addEventListener('change',renderReview));
renderReview();
