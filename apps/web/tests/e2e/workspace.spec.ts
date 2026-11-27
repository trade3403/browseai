import {test,expect} from '@playwright/test'
test('project switch, tabs and terminal controls',async({page})=>{
  await page.goto('/')
  await expect(page.locator('.project')).toHaveCount(6)
  await page.locator('.project').filter({hasText:'GlobalChat'}).click()
  await expect(page.locator('.state-chip')).toContainText('WAIT_CI')
  await page.getByRole('button',{name:'暂停任务'}).click()
  await expect(page.locator('.state-chip')).toContainText('PAUSED')
  await page.getByRole('button',{name:'恢复运行'}).click()
  await expect(page.locator('.state-chip')).toContainText('WAIT_CI')
  await page.getByRole('button',{name:'人工接管'}).click()
  await expect(page.getByRole('button',{name:'退出接管'})).toBeVisible()
  await page.getByRole('button',{name:'结束任务'}).click()
  await expect(page.locator('.state-chip')).toContainText('STOPPED')
  await expect(page.getByRole('button',{name:'恢复运行'})).toBeDisabled()
  await page.getByRole('button',{name:'关闭 GlobalChat'}).click()
  await expect(page.locator('.tab')).toHaveCount(2)
  await page.getByRole('button',{name:'新增标签'}).click()
  await expect(page.locator('.tab')).toHaveCount(3)
})
test('split, full screen and mobile layout',async({page})=>{
  await page.goto('/')
  await expect(page.locator('.project')).toHaveCount(6)
  await page.getByRole('button',{name:'分屏'}).click()
  await expect(page.locator('.browser')).toHaveCount(2)
  await page.getByRole('button',{name:'全屏',exact:true}).click()
  await expect(page.locator('.shell')).toHaveClass(/expanded/)
  await page.getByRole('button',{name:'退出全屏'}).click()
  for(const size of [{width:1920,height:1080},{width:1366,height:768},{width:390,height:844}]){
    await page.setViewportSize(size)
    await expect(page.locator('.workspace')).toBeVisible()
    await expect(page.getByRole('button',{name:'新建窗口'})).toBeVisible()
    expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(size.width+1)
  }
})
test('API outage is shown rather than fake success',async({page})=>{
  await page.route('**/api/projects',route=>route.fulfill({status:503,body:'unavailable'}))
  await page.goto('/')
  await expect(page.getByText('API 503')).toBeVisible()
  await expect(page.locator('.project')).toHaveCount(0)
})

test('mock CI is explicitly marked and does not masquerade as GitHub evidence',async({page})=>{
  await page.goto('/')
  await expect(page.locator('.online')).toContainText('模拟')
  await expect(page.locator('.ci-state')).toContainText('模拟 CI 通过')
  await expect(page.locator('.ci-state')).toContainText('非真实 CI')
  await page.locator('.project').filter({hasText:'GlobalChat'}).click()
  await expect(page.locator('.ci-state')).toContainText('模拟等待 CI')
  await expect(page.locator('.ci-demo-passed')).toHaveCount(0)
})

test('reference sample fields stay scoped to the mock project and details are labeled',async({page})=>{
  await page.goto('/')
  await expect(page.locator('.project')).toHaveCount(6)
  await expect(page.getByRole('link',{name:'trade3403/localtunnel'})).toHaveAttribute('href','https://github.com/trade3403/localtunnel')
  await expect(page.getByText('V0.8（第 8 / 10 阶段，示例）',{exact:false})).toBeVisible()
  await page.getByRole('button',{name:'查看示例'}).click()
  await expect(page.locator('.notice')).toContainText('不代表 GitHub 实际结果')
  await page.locator('.project').filter({hasText:'GlobalChat'}).click()
  await expect(page.getByText('未连接真实任务计划',{exact:false})).toBeVisible()
  await expect(page.locator('.ci-demo-passed')).toHaveCount(0)
})

test('desktop footer labels do not overlap adjacent status fields',async({page})=>{
  await page.setViewportSize({width:1536,height:864})
  await page.goto('/')
  await expect(page.locator('.project')).toHaveCount(6)
  const fields=await page.locator('.shell footer > span:not(.footer-spacer)').evaluateAll(nodes=>nodes.map(node=>{
    const rect=node.getBoundingClientRect()
    const range=document.createRange()
    range.selectNodeContents(node)
    const content=range.getBoundingClientRect()
    return {left:rect.left,right:rect.right,contentRight:content.right}
  }))
  for(let i=0;i<fields.length-1;i++){
    expect(fields[i].right).toBeLessThanOrEqual(fields[i+1].left+1)
    expect(fields[i].contentRight).toBeLessThanOrEqual(fields[i+1].left+1)
  }
  await expect(page.locator('.footer-duration')).toContainText('示例')
})
