'use strict';
document.getElementById('asset-theme').addEventListener('change',event=>{document.documentElement.dataset.theme=event.target.value;});
document.getElementById('asset-grey').addEventListener('change',event=>{document.documentElement.classList.toggle('is-greyscale',event.target.checked);});
