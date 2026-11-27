import {defineConfig} from '@playwright/test'
export default defineConfig({
  testDir:'./tests/e2e',
  reporter:[['list'],['html',{open:'never'}]],
  use:{baseURL:'http://127.0.0.1:5173',viewport:{width:1536,height:864},deviceScaleFactor:1,locale:'zh-CN',timezoneId:'UTC'},
  webServer:[
    {command:'go run .',cwd:'../../services/controller',url:'http://127.0.0.1:8080/healthz',reuseExistingServer:false,timeout:120000},
    {command:'npm run dev -- --port 5173 --strictPort',url:'http://127.0.0.1:5173',reuseExistingServer:false,timeout:120000}
  ],
  projects:[{name:'functional',testMatch:'workspace.spec.ts'},{name:'visual',testMatch:'visual.spec.ts'}]
})
