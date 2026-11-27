import {test,expect} from '@playwright/test'
import {readFile,mkdir,writeFile} from 'node:fs/promises'
import {createHash} from 'node:crypto'
import path from 'node:path'
import sharp from 'sharp'
import pixelmatch from 'pixelmatch'
import {PNG} from 'pngjs'
const root=path.resolve(process.cwd(),'../..')
test('authoritative desktop visual baseline',async({page},info)=>{
  const manifest=JSON.parse(await readFile(path.join(root,'design/visual-baseline.json'),'utf8'))
  const profile=process.env.BROWSEAI_VISUAL_PROFILE ?? 'development'
  if(profile!=='development'&&profile!=='release')throw new Error('Invalid visual acceptance profile: '+profile)
  const developmentLimit=manifest.visual_ci.max_diff_ratio as number
  const releaseLimit=manifest.visual_ci.release_max_diff_ratio as number
  // User-approved policy. No environment variable may override the numeric limits.
  expect(developmentLimit).toBe(0.15)
  expect(releaseLimit).toBe(0.01)
  expect(manifest.visual_ci.threshold).toBe(0.1)
  expect(manifest.visual_ci.max_boundary_error_css_px).toBe(2)
  const activeLimit=profile==='release'?releaseLimit:developmentLimit
  const out=info.outputPath('visual')
  await mkdir(out,{recursive:true})
  await page.goto('/')
  await expect(page.locator('.project')).toHaveCount(6)
  await page.evaluate(()=>document.fonts.ready)
  const actual=await page.screenshot({animations:'disabled',path:path.join(out,'actual.png')})
  let source:Buffer
  try{source=await readFile(path.join(root,manifest.reference))}
  catch{await writeFile(path.join(out,'report.json'),JSON.stringify({status:'BLOCKED',reason:'missing authoritative reference'},null,2));throw new Error('BLOCKED: missing original binary reference '+manifest.reference)}
  const actualHash=createHash('sha256').update(source).digest('hex')
  if(actualHash!==manifest.sha256){await writeFile(path.join(out,'report.json'),JSON.stringify({status:'BLOCKED',reason:'reference hash mismatch',expected:manifest.sha256,actual:actualHash},null,2));throw new Error('BLOCKED: reference hash mismatch')}
  const meta=await sharp(source).metadata()
  expect([meta.width,meta.height,meta.format]).toEqual([1536,864,'webp'])
  const ref=await sharp(source).png().toBuffer()
  await writeFile(path.join(out,'reference.png'),ref)
  const a=PNG.sync.read(actual),b=PNG.sync.read(ref)
  expect([a.width,a.height]).toEqual([b.width,b.height])
  const diff=new PNG({width:a.width,height:a.height})
  const entire=pixelmatch(a.data,b.data,diff.data,a.width,a.height,{threshold:manifest.visual_ci.threshold,includeAA:true})
  await writeFile(path.join(out,'diff.png'),PNG.sync.write(diff))
  const remote=manifest.visual_ci.regions.remote_viewport as number[]
  const within=(x:number,y:number)=>x>=remote[0]&&x<remote[0]+remote[2]&&y>=remote[1]&&y<remote[1]+remote[3]
  const scores:Record<string,{pixels:number;diff:number;ratio:number;strictDiff:number}>={}
  const hotspots:Record<string,{x:number;y:number;width:number;height:number;diff:number;ratio:number}[]>={}
  for(const [name,box] of Object.entries(manifest.visual_ci.regions)){
    const [x,y,w,h]=box as number[]
    let total=0,changed=0,strict=0
    // A second region comparison uses the same pixelmatch threshold and antialias policy.
    const pa=new PNG({width:w,height:h}),pb=new PNG({width:w,height:h}),pd=new PNG({width:w,height:h})
    for(let j=0;j<h;j++)for(let i=0;i<w;i++){
      const from=((y+j)*a.width+x+i)*4,to=(j*w+i)*4
      a.data.copy(pa.data,to,from,from+4);b.data.copy(pb.data,to,from,from+4)
      if(!a.data.subarray(from,from+4).equals(b.data.subarray(from,from+4)))strict++
    }
    total=w*h;changed=pixelmatch(pa.data,pb.data,pd.data,w,h,{threshold:manifest.visual_ci.threshold,includeAA:true})
    scores[name]={pixels:total,diff:changed,ratio:changed/total,strictDiff:strict}
    // Diagnostic-only mask, same threshold and AA policy. Acceptance metrics above are unchanged.
    const mask=new PNG({width:w,height:h})
    pixelmatch(pa.data,pb.data,mask.data,w,h,{threshold:manifest.visual_ci.threshold,includeAA:true,diffMask:true})
    const tiles=[]
    const step=48
    for(let ty=0;ty<h;ty+=step)for(let tx=0;tx<w;tx+=step){
      const tw=Math.min(step,w-tx),th=Math.min(step,h-ty)
      let count=0
      for(let py=ty;py<ty+th;py++)for(let px=tx;px<tx+tw;px++)if(mask.data[(py*w+px)*4+3]!==0)count++
      tiles.push({x:x+tx,y:y+ty,width:tw,height:th,diff:count,ratio:count/(tw*th)})
    }
    hotspots[name]=tiles.sort((a,b)=>b.diff-a.diff).slice(0,12)
  }
  // Compare shell without altering the original images; exclude only the declared remote viewport.
  const ca=Buffer.from(a.data),cb=Buffer.from(b.data)
  let shellPixels=0
  for(let y=0;y<a.height;y++)for(let x=0;x<a.width;x++){
    if(within(x,y)){const off=(y*a.width+x)*4;ca.fill(0,off,off+4);cb.fill(0,off,off+4)}else shellPixels++
  }
  const shellDiff=pixelmatch(ca,cb,null,a.width,a.height,{threshold:manifest.visual_ci.threshold,includeAA:true})
  const bound=await page.locator('.browser-grid').boundingBox()
  const boundaryError=bound?Math.max(Math.abs(bound.x-remote[0]),Math.abs(bound.y-remote[1]),Math.abs(bound.width-remote[2]),Math.abs(bound.height-remote[3])):null
  const geometry=await page.evaluate(()=>{
    const selectors=['.global-header','.sidebar','.sidebar nav','.section-heading','.project','.workspace-toolbar','.tab','.remote-status','.browser-grid','.controls','.right-panel section','.shell footer','.shell footer span']
    return Object.fromEntries(selectors.map(selector=>[selector,Array.from(document.querySelectorAll(selector)).map(el=>{
      const {x,y,width,height}=el.getBoundingClientRect()
      return {x,y,width,height}
    })]))
  })
  const typography=await page.evaluate(()=>Object.fromEntries(['.brand strong','.tagline','.project-head b','.tab button','.right-panel h3','.shell footer'].map(selector=>{
    const el=document.querySelector(selector)
    if(!el)return [selector,null]
    const s=getComputedStyle(el)
    return [selector,{family:s.fontFamily,size:s.fontSize,weight:s.fontWeight,lineHeight:s.lineHeight,letterSpacing:s.letterSpacing}]
  })))
  const shellRatio=shellDiff/shellPixels
  const boundaryPassed=boundaryError!==null&&boundaryError<=manifest.visual_ci.max_boundary_error_css_px
  const passedAt=(limit:number)=>boundaryPassed&&shellRatio<=limit&&Object.entries(scores).every(([name,score])=>name==='remote_viewport'||score.ratio<=limit)
  const acceptance={profile,activeLimit,developmentLimit,releaseLimit,boundaryPassed,developmentPassed:passedAt(developmentLimit),releasePassed:passedAt(releaseLimit)}
  const report={entireRatio:entire/(a.width*a.height),shellRatio,excluded:remote,scores,boundaryError,geometry,hotspots,typography,acceptance}
  await writeFile(path.join(out,'report.json'),JSON.stringify(report,null,2))
  console.log('VISUAL_ACCEPTANCE',JSON.stringify({shellRatio,boundaryError,regions:Object.fromEntries(Object.entries(scores).map(([name,score])=>[name,score.ratio])),...acceptance}))
  expect.soft(report.shellRatio,profile+' shell').toBeLessThanOrEqual(activeLimit)
  for(const [name,score] of Object.entries(scores))if(name!=='remote_viewport')expect.soft(score.ratio,profile+' '+name).toBeLessThanOrEqual(activeLimit)
  expect(boundaryError).not.toBeNull()
  expect(boundaryError!).toBeLessThanOrEqual(manifest.visual_ci.max_boundary_error_css_px)
})

/* Diagnostics are deliberately separate from the authoritative test above.
 * Never substitute a candidate image for actual.png or use its score as the gate. */
test('diagnostic font stack comparison',async({page},info)=>{
  test.setTimeout(120000)
  const manifest=JSON.parse(await readFile(path.join(root,'design/visual-baseline.json'),'utf8'))
  const original=await readFile(path.join(root,manifest.reference))
  expect(createHash('sha256').update(original).digest('hex')).toBe(manifest.sha256)
  const reference=PNG.sync.read(await sharp(original).png().toBuffer())
  const out=info.outputPath('font-diagnostics')
  await mkdir(out,{recursive:true})
  await page.goto('/')
  await expect(page.locator('.project')).toHaveCount(6)
  const remote=manifest.visual_ci.regions.remote_viewport as number[]
  const [rx,ry,rw,rh]=remote
  const shellPixels=reference.width*reference.height-rw*rh
  const fixed=Buffer.from(reference.data)
  for(let y=ry;y<ry+rh;y++)fixed.fill(0,(y*reference.width+rx)*4,(y*reference.width+rx+rw)*4)
  const candidates=[
    {name:'current',css:''},
    {name:'noto-first',css:'html,body,#app{font-family:"Noto Sans CJK SC",Arial,sans-serif}'},
    {name:'system-first',css:'html,body,#app{font-family:system-ui,"Noto Sans CJK SC",sans-serif}'},
    {name:'liberation-first',css:'html,body,#app{font-family:"Liberation Sans","Noto Sans CJK SC",sans-serif}'},
    {name:'yahei-first',css:'html,body,#app{font-family:"Microsoft YaHei","Noto Sans CJK SC",Arial,sans-serif}'}
  ]
  const session=await page.context().newCDPSession(page)
  await session.send('DOM.enable')
  await session.send('CSS.enable')
  const result=[]
  for(const candidate of candidates){
    const style=candidate.css?await page.addStyleTag({content:candidate.css}):null
    await page.evaluate(()=>document.fonts.ready)
    const shot=await page.screenshot({animations:'disabled',path:path.join(out,candidate.name+'.png')})
    const actual=PNG.sync.read(shot)
    expect([actual.width,actual.height]).toEqual([reference.width,reference.height])
    const masked=Buffer.from(actual.data)
    for(let y=ry;y<ry+rh;y++)masked.fill(0,(y*actual.width+rx)*4,(y*actual.width+rx+rw)*4)
    const ratio=pixelmatch(masked,fixed,null,actual.width,actual.height,{threshold:manifest.visual_ci.threshold,includeAA:true})/shellPixels
    const regions:Record<string,number>={}
    for(const [name,box] of Object.entries(manifest.visual_ci.regions)){
      if(name==='remote_viewport')continue
      const [x,y,w,h]=box as number[]
      const a=Buffer.alloc(w*h*4),b=Buffer.alloc(w*h*4)
      for(let j=0;j<h;j++){
        const start=((y+j)*actual.width+x)*4
        actual.data.copy(a,j*w*4,start,start+w*4)
        reference.data.copy(b,j*w*4,start,start+w*4)
      }
      regions[name]=pixelmatch(a,b,null,w,h,{threshold:manifest.visual_ci.threshold,includeAA:true})/(w*h)
    }
    const platformFonts:Record<string,unknown>={}
    const {root:dom}=await session.send('DOM.getDocument')
    for(const selector of ['.brand strong','.tagline','.project-head b','.tab button','.right-panel h3','.right-panel dd','.shell footer']){
      const {nodeId}=await session.send('DOM.querySelector',{nodeId:dom.nodeId,selector})
      if(nodeId){
        try{platformFonts[selector]=(await session.send('CSS.getPlatformFontsForNode',{nodeId})).fonts}
        catch{platformFonts[selector]=null}
      }
    }
    const bounds=await page.locator('.browser-grid').boundingBox()
    const boundaryError=bounds?Math.max(Math.abs(bounds.x-rx),Math.abs(bounds.y-ry),Math.abs(bounds.width-rw),Math.abs(bounds.height-rh)):null
    result.push({name:candidate.name,css:candidate.css,shellRatio:ratio,regions,platformFonts,boundaryError})
    if(style)await style.evaluate(el=>el.remove())
  }
  await session.detach()
  await writeFile(path.join(out,'font-report.json'),JSON.stringify({diagnosticOnly:true,baseline:'current',results:result,ranking:[...result].sort((a,b)=>a.shellRatio-b.shellRatio).map(x=>({name:x.name,shellRatio:x.shellRatio,boundaryError:x.boundaryError}))},null,2))
  console.log('FONT_DIAGNOSTICS',JSON.stringify(result.map(x=>({name:x.name,shellRatio:x.shellRatio,boundaryError:x.boundaryError}))))
})

/* Region-level layout experiments. These are diagnostic only: the original
 * baseline test, reference, threshold and exclusion are never modified.
 * All candidates render the same functional application and mock data. */
test('diagnostic layout comparison by region',async({page},info)=>{
  test.setTimeout(120000)
  const manifest=JSON.parse(await readFile(path.join(root,'design/visual-baseline.json'),'utf8'))
  const bytes=await readFile(path.join(root,manifest.reference))
  expect(createHash('sha256').update(bytes).digest('hex')).toBe(manifest.sha256)
  const reference=PNG.sync.read(await sharp(bytes).png().toBuffer())
  const out=info.outputPath('layout-diagnostics')
  await mkdir(out,{recursive:true})
  await page.goto('/')
  await expect(page.locator('.project')).toHaveCount(6)
  const candidates=[
    {name:'current',css:''},
    {name:'header-type-1',css:'.brand strong{font-size:19px;letter-spacing:0}.tagline{font-size:13.5px;letter-spacing:-.1px}'},
    {name:'header-type-2',css:'.brand strong{font-size:18px;letter-spacing:.55px}.tagline{font-size:12.5px;letter-spacing:0}'},
    {name:'header-spacing',css:'.header-actions{gap:12px}.header-actions .online{min-width:97px}.user-avatar{width:26px;height:26px}.global-header{gap:34px}'},
    {name:'sidebar-type-1',css:'.sidebar nav button{font-size:13.5px;font-weight:600}.sidebar .section-heading{font-size:14px;font-weight:500}.project-head b{font-size:14px}'},
    {name:'sidebar-type-2',css:'.sidebar nav button{font-size:14px;font-weight:600}.sidebar .section-heading{font-size:15px;font-weight:500}.project-head b{font-size:14.5px}'},
    {name:'toolbar-type-1',css:'.workspace-toolbar strong{font-size:14px;font-weight:700}.tab button{font-size:13.5px}.toolbar-buttons button{font-size:12px}.remote-status>span{font-size:12px}'},
    {name:'toolbar-type-2',css:'.workspace-toolbar strong{font-size:13.5px}.tab button{font-size:13px}.toolbar-buttons button{font-size:11.5px}.remote-status>span{font-size:12px;font-weight:600}'},
    {name:'right-type-1',css:'.right-panel h3{font-size:13.5px}.right-panel dl{font-size:12.5px}.right-panel .next-actions li{font-size:12.5px}.right-panel .task-details dd{font-size:12px}'},
    {name:'right-type-2',css:'.right-panel h3{font-size:13px}.right-panel dl{font-size:13px}.right-panel .next-actions li{font-size:13px}.right-panel .task-details dd{font-size:12px}'},
    {name:'footer-type-1',css:'.shell footer>span{font-size:13px}.shell footer>span:nth-of-type(8){font-size:12px}'},
    {name:'footer-type-2',css:'.shell footer>span{font-size:12.5px;font-weight:500}.shell footer>span:nth-of-type(8){font-size:11.5px}'}
  ]
  const [rx,ry,rw,rh]=manifest.visual_ci.regions.remote_viewport as number[]
  const fixed=Buffer.from(reference.data)
  for(let y=ry;y<ry+rh;y++)fixed.fill(0,(y*reference.width+rx)*4,(y*reference.width+rx+rw)*4)
  const result=[]
  for(const candidate of candidates){
    const style=candidate.css?await page.addStyleTag({content:candidate.css}):null
    await page.evaluate(()=>document.fonts.ready)
    const screenshot=await page.screenshot({animations:'disabled',path:path.join(out,candidate.name+'.png')})
    const actual=PNG.sync.read(screenshot)
    expect([actual.width,actual.height]).toEqual([reference.width,reference.height])
    const masked=Buffer.from(actual.data)
    for(let y=ry;y<ry+rh;y++)masked.fill(0,(y*actual.width+rx)*4,(y*actual.width+rx+rw)*4)
    const shellRatio=pixelmatch(masked,fixed,null,actual.width,actual.height,{threshold:manifest.visual_ci.threshold,includeAA:true})/(actual.width*actual.height-rw*rh)
    const regions:Record<string,number>={}
    for(const [name,box] of Object.entries(manifest.visual_ci.regions)){
      if(name==='remote_viewport')continue
      const [x,y,w,h]=box as number[]
      const a=Buffer.alloc(w*h*4),b=Buffer.alloc(w*h*4)
      for(let j=0;j<h;j++){
        const start=((y+j)*actual.width+x)*4
        actual.data.copy(a,j*w*4,start,start+w*4)
        reference.data.copy(b,j*w*4,start,start+w*4)
      }
      regions[name]=pixelmatch(a,b,null,w,h,{threshold:manifest.visual_ci.threshold,includeAA:true})/(w*h)
    }
    const bounds=await page.locator('.browser-grid').boundingBox()
    const boundaryError=bounds?Math.max(Math.abs(bounds.x-rx),Math.abs(bounds.y-ry),Math.abs(bounds.width-rw),Math.abs(bounds.height-rh)):null
    const overflow=await page.evaluate(()=>{
      const sections=Array.from(document.querySelectorAll('.right-panel section'))
      return sections.some(section=>section.scrollHeight>section.clientHeight+2)||document.documentElement.scrollWidth>innerWidth+1
    })
    result.push({name:candidate.name,css:candidate.css,shellRatio,regions,boundaryError,overflow})
    if(style)await style.evaluate(el=>el.remove())
  }
  const ranking=Object.fromEntries(Object.keys(result[0].regions).map(name=>[name,[...result].sort((a,b)=>a.regions[name]-b.regions[name]).map(x=>({name:x.name,ratio:x.regions[name],shellRatio:x.shellRatio,boundaryError:x.boundaryError,overflow:x.overflow}))]))
  await writeFile(path.join(out,'layout-report.json'),JSON.stringify({diagnosticOnly:true,results:result,ranking},null,2))
  console.log('LAYOUT_DIAGNOSTICS',JSON.stringify(result.map(x=>({name:x.name,shellRatio:x.shellRatio,regions:x.regions,boundaryError:x.boundaryError,overflow:x.overflow}))))
})
