import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'tests/browser',timeout:30000,use:{baseURL:process.env.MRKT_BROWSER_URL||'http://127.0.0.1:8080',browserName:'chromium',launchOptions:{executablePath:'/usr/bin/chromium',args:['--no-sandbox']},trace:'retain-on-failure'},reporter:'list'});
