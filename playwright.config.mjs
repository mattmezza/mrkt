import {existsSync} from 'node:fs';
import {defineConfig} from '@playwright/test';

const executablePath=process.env.MRKT_CHROMIUM_PATH||
  (existsSync('/usr/bin/chromium')?'/usr/bin/chromium':undefined);
export default defineConfig({testDir:'tests/browser',timeout:30000,use:{baseURL:process.env.MRKT_BROWSER_URL||'http://127.0.0.1:8080',browserName:'chromium',launchOptions:{...(executablePath?{executablePath}:{}),args:['--no-sandbox']},trace:'retain-on-failure'},reporter:'list'});
