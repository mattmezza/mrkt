import {chromium} from '@playwright/test';
import {readFile,mkdir,copyFile} from 'node:fs/promises';
import {resolve} from 'node:path';
await mkdir('assets/brand',{recursive:true});
await copyFile('internal/httpapi/static/icon.svg','assets/brand/icon.svg');
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH||'/usr/bin/chromium',headless:true,args:['--no-sandbox']});
const page=await browser.newPage({viewport:{width:1280,height:640},deviceScaleFactor:1});
await page.goto('file://'+resolve('assets/brand/cover.svg'));
await page.screenshot({path:'assets/brand/cover.png'});
await copyFile('assets/brand/cover.png','assets/brand/social-preview.png');
await page.goto('about:blank');
for(const size of [32,180,192,512]){await page.setViewportSize({width:size,height:size});await page.setContent(`<style>html,body{margin:0;width:100%;height:100%}svg{width:100%;height:100%}</style>${await readFile('assets/brand/icon.svg','utf8')}`);await page.screenshot({path:`assets/brand/icon-${size}.png`})}
await browser.close();
