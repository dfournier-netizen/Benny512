'use strict';
const assetQuery=new URLSearchParams(location.search);
document.documentElement.dataset.theme=assetQuery.get('theme')==='light'?'light':'dark';
document.getElementById('theme').value=document.documentElement.dataset.theme;
document.documentElement.classList.toggle('grey',assetQuery.get('grey')==='1');
document.getElementById('grey').checked=assetQuery.get('grey')==='1';
document.getElementById('theme').addEventListener('change',ev=>document.documentElement.dataset.theme=ev.target.value);
document.getElementById('grey').addEventListener('change',ev=>document.documentElement.classList.toggle('grey',ev.target.checked));
document.querySelector('.toggle').addEventListener('click',ev=>{const b=ev.currentTarget;b.setAttribute('aria-pressed',String(b.getAttribute('aria-pressed')!=='true'));});
