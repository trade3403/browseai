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
  const report={entireRatio:entire/(a.width*a.height),shellRatio:shellDiff/shellPixels,excluded:remote,scores,boundaryError,geometry,hotspots,typography}
  await writeFile(path.join(out,'report.json'),JSON.stringify(report,null,2))
  expect.soft(report.shellRatio).toBeLessThanOrEqual(manifest.visual_ci.max_diff_ratio)
  for(const [name,score] of Object.entries(scores))if(name!=='remote_viewport')expect.soft(score.ratio,name).toBeLessThanOrEqual(manifest.visual_ci.max_diff_ratio)
  expect(boundaryError).not.toBeNull()
  expect(boundaryError!).toBeLessThanOrEqual(manifest.visual_ci.max_boundary_error_css_px)
})
