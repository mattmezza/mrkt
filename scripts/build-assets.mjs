import { mkdir, copyFile, readdir } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
const out='internal/httpapi/static';
await mkdir(out+'/fonts',{recursive:true});
const result=spawnSync('node',['node_modules/@tailwindcss/cli/dist/index.mjs','-i','internal/httpapi/styles.css','-o',out+'/app.css','--minify'],{stdio:'inherit'});
if(result.status!==0) process.exit(result.status||1);
await copyFile('node_modules/htmx.org/dist/htmx.min.js',out+'/htmx.min.js');
await copyFile('node_modules/@alpinejs/csp/dist/cdn.min.js',out+'/alpine.min.js');
for(const family of ['space-grotesk','inter']){
 const dir=`node_modules/@fontsource/${family}/files`;
 for(const name of await readdir(dir)) if(name.endsWith('.woff2') && /-(latin|latin-ext)-(400|600|700)-normal/.test(name)) await copyFile(dir+'/'+name,out+'/fonts/'+name);
}
