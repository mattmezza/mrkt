document.addEventListener('alpine:init',()=>{Alpine.data('disclosure',()=>({open:false,toggle(){this.open=!this.open}}));});
function wire(){
 const menu=document.querySelector('.sidebar-menu');
 if(menu)menu.open=matchMedia('(min-width: 60rem)').matches;
 const dialog=document.querySelector('#view-search'),trigger=document.querySelector('#open-search'),query=document.querySelector('#view-query');
 if(!dialog||!trigger)return;
 trigger.onclick=()=>{dialog.showModal();query.focus()};
 query.oninput=()=>{for(const link of dialog.querySelectorAll('nav a'))link.hidden=!link.textContent.toLowerCase().includes(query.value.toLowerCase())};
 dialog.onclick=e=>{if(e.target===dialog)dialog.close()};
 dialog.onkeydown=e=>{if(!['ArrowDown','ArrowUp'].includes(e.key))return;e.preventDefault();const links=[...dialog.querySelectorAll('nav a')].filter(a=>!a.hidden);const index=links.indexOf(document.activeElement);links[(index+(e.key==='ArrowDown'?1:links.length-1))%links.length]?.focus()};
}
document.addEventListener('DOMContentLoaded',wire);
document.addEventListener('htmx:afterSwap',wire);
document.addEventListener('keydown',e=>{if((e.metaKey||e.ctrlKey)&&e.key==='k'){e.preventDefault();document.querySelector('#open-search')?.click()}});
