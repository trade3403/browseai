<script setup lang="ts">
import {computed, onMounted, ref} from 'vue'
import {Cloud, Home, ListTodo, PanelsTopLeft, Github, ScrollText, Settings, Plus, Bell, X, Monitor, Keyboard, Mouse, RefreshCw, Maximize, Columns2, Pause, Play, User, Square, CheckCircle2, Circle, ArrowRight, Radio, Wifi, Clock, ExternalLink, ChevronDown} from 'lucide-vue-next'
import GitHubMark from './components/GitHubMark.vue'
import {labels,transition} from './state'
import type {Project,Action,State} from './state'

const nav = [{name:'项目总览',icon:Home},{name:'任务调度',icon:ListTodo},{name:'浏览器窗口',icon:PanelsTopLeft},{name:'GitHub 监控',icon:Github},{name:'运行日志',icon:ScrollText},{name:'系统设置',icon:Settings}]
const projects = ref<Project[]>([])
const tabs = ref<string[]>([])
const current = ref('')
const activeNav = ref('项目总览')
const loading = ref(true)
const error = ref('')
const previous = new Map<string,State>()
const split = ref(false)
const expanded = ref(false)
const takeover = ref(false)
const notices = ref<string[]>([])
const showNotices = ref(false)
const selected = computed(()=>projects.value.find(p=>p.id===current.value))
const tabProjects = computed(()=>tabs.value.map(id=>projects.value.find(p=>p.id===id)).filter((p):p is Project=>!!p))
const secondary = computed(()=>tabProjects.value.find(p=>p.id!==current.value))
const state = computed(()=>selected.value?.state || 'QUEUED')
const demoProject = computed(()=>selected.value?.id==='localtunnel')
const repoURL = computed(()=>{const name=selected.value?.repository;return name && /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(name) ? 'https://github.com/'+name : ''})
const demoPassed = computed(()=>selected.value?.id==='localtunnel' && state.value==='DEVELOPING')
const terminal = computed(()=>state.value==='STOPPED'||state.value==='COMPLETED')
const statusColor = (s:State)=>s==='DEVELOPING'||s==='COMPLETED'?'green':s==='WAIT_CI'||s==='PAUSED'?'yellow':'muted'
function log(s:string){notices.value.unshift(s)}
function open(id:string){if(!tabs.value.includes(id)) tabs.value.push(id);current.value=id;takeover.value=false}
function close(id:string){tabs.value=tabs.value.filter(x=>x!==id);if(current.value===id){current.value=tabs.value[0]||'';takeover.value=false}}
function nextWindow(){const p=projects.value.find(p=>!tabs.value.includes(p.id));if(p)open(p.id);else log('所有示例项目均已打开')}
function act(a:Action){const p=selected.value;if(!p)return;const old=p.state;if(a==='pause'&&old!=='PAUSED')previous.set(p.id,old);p.state=transition(old,a,previous.get(p.id));if(old!==p.state)log(p.name+'：'+labels[p.state]);if(a==='stop')takeover.value=false}
async function load(){
  loading.value=true;error.value=''
  try{const r=await fetch('/api/projects');if(!r.ok)throw new Error('API '+r.status);const data=await r.json();if(data.mock!==true||!Array.isArray(data.projects))throw new Error('非预期数据');projects.value=data.projects;tabs.value=projects.value.slice(0,3).map(p=>p.id);current.value=tabs.value[0]||'';takeover.value=false;log('模拟数据已加载')}
  catch(e){error.value=e instanceof Error?e.message:'加载失败'}finally{loading.value=false}
}
onMounted(load)
</script>
<template>
  <div class="shell" :class="{expanded}">
    <header class="global-header">
      <div class="brand"><svg class="brand-mark" width="39" height="32" viewBox="0 0 40 32" fill="none" aria-hidden="true"><path d="M12 26h19a7 7 0 0 0 0-14h-1A11 11 0 0 0 9 14a6 6 0 0 0 3 12Z" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/><path d="M15 19l3-3 3 3m-3-3v7m6-3h4" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/></svg><strong>AI Browser 云端开发管理平台</strong><span class="version">v0.1.0</span></div>
      <span class="tagline">基于 ChatGPT 网页的自动化开发助手 · 云端运行 · 本地实时查看 · 不调用付费 AI API</span>
      <div class="header-actions"><span class="online" title="本地模拟工作站，未连接真实云端浏览器或 GitHub"><Circle :size="10" fill="currentColor"/> 模拟模式</span><button class="notification-button" title="通知" :aria-label="'通知，' + notices.length + '条本地日志'" @click="showNotices=!showNotices"><Bell :size="19"/><span v-if="notices.length" class="notice-count">{{Math.min(notices.length,99)}}</span></button><button title="设置" @click="activeNav='系统设置';showNotices=false"><Settings :size="19"/></button><span class="user-avatar" title="本地模拟用户"><User :size="16"/></span><span>本地用户</span><ChevronDown :size="12"/></div>
    </header>
    <aside class="sidebar">
      <nav><button v-for="item in nav" :key="item.name" :class="{active:activeNav===item.name}" @click="activeNav=item.name"><GitHubMark v-if="item.name==='GitHub 监控'" :size="18"/><component :is="item.icon" v-else :size="18"/>{{item.name}}</button></nav>
      <div class="section-heading">项目列表 ({{projects.length}})<button title="打开下一个项目" :disabled="!projects.length" @click="nextWindow"><Plus :size="17"/></button></div>
      <div class="project-list">
        <button v-for="p in projects" :key="p.id" class="project" :class="{selected:p.id===current}" :aria-pressed="p.id===current" @click="open(p.id);activeNav='项目总览'">
          <div class="project-head"><GitHubMark :size="19"/><b>{{p.name}}</b><span :class="statusColor(p.state)"><CheckCircle2 v-if="p.state==='COMPLETED'" :size="12" class="project-check"/><i v-else class="project-status-dot" aria-hidden="true"></i> {{labels[p.state]}}</span></div>
          <div class="project-progress"><div class="track"><div :style="{width:p.progress+'%'}" :class="{complete:p.state==='COMPLETED'}"></div></div><span>{{p.progress}}%</span></div>
        </button>
      </div>
    </aside>
    <main class="workspace">
      <div class="workspace-toolbar">
        <strong><PanelsTopLeft :size="19"/> 远程浏览器窗口</strong>
        <div class="tabs">
          <div v-for="p in tabProjects" :key="p.id" class="tab" :class="{active:p.id===current}"><button :aria-pressed="p.id===current" @click="open(p.id);activeNav='项目总览'"><i :class="[statusColor(p.state),'tab-status-dot']" aria-hidden="true"></i> {{p.name}}</button><button :aria-label="'关闭 '+p.name" @click="close(p.id)"><X :size="13"/></button></div>
          <button aria-label="新增标签" @click="nextWindow"><Plus :size="18"/></button>
        </div>
        <div class="toolbar-buttons"><button :aria-pressed="split" @click="split=!split"><Columns2 :size="15"/> 分屏</button><button :aria-pressed="expanded" @click="expanded=!expanded"><Maximize :size="15"/> {{expanded?'退出全屏':'全屏'}}</button><button @click="nextWindow"><Plus :size="14"/> 新建窗口</button></div>
      </div>
      <div class="remote">
        <div class="remote-status"><span><i class="green remote-status-dot" aria-hidden="true"></i> 云端 Chrome（模拟） · 分辨率 1920×1080 · 延迟未知</span><div><span class="live"><Monitor :size="13"/> 模拟画面</span><button disabled><Keyboard :size="13"/> 键盘</button><button disabled><Mouse :size="13"/> 鼠标</button><button @click="load"><RefreshCw :size="13"/> 刷新</button></div></div>
        <div v-if="loading" class="empty">正在加载模拟项目…</div>
        <div v-else-if="error" class="empty"><p>{{error}}</p><button @click="load">重试</button></div>
        <div v-else-if="activeNav!=='项目总览' && activeNav!=='浏览器窗口'" class="empty"><h2>{{activeNav}}</h2><p>V0.1 模拟工作站；此模块尚未接入真实服务。</p><ul v-if="activeNav==='运行日志'"><li v-for="(line,i) in notices" :key="i">{{line}}</li></ul><button @click="activeNav='项目总览'">返回工作区</button></div>
        <div v-else-if="selected" class="browser-grid" :class="{split:split&&secondary}">
          <div v-for="p in (split&&secondary?[selected,secondary]:[selected])" :key="p.id" class="browser">
            <div class="browser-chrome"><div class="browser-tabs"><span class="window-dots"><i></i><i></i><i></i></span><span class="browser-tab"><Circle :size="13"/> 模拟 Chat 窗口 <X :size="12"/></span><Plus :size="14"/></div><div class="address"><span>‹　›　↻</span><span class="address-value">本地测试站点 · 不连接 ChatGPT</span></div></div>
            <div class="mock-chat"><div class="mock-sidebar"><b>Chat 工作区</b><p>新建聊天</p><p>搜索聊天</p><small>项目</small><p class="mock-selected">{{p.name}}</p><small>历史</small><p>下一阶段开发</p><p>修复 CI 测试问题</p><p>项目架构设计</p></div><div class="chat-body"><div class="chat-title">模拟会话 <span>只读预览</span></div><div class="bubble">当前项目：{{p.name}}<br>请检查本阶段开发进度与 CI 验收条件。</div><div class="reply"><b>V0.1 本地模拟界面</b><p>此区域仅用于界面与交互测试。</p><p>真实远程浏览器将在 V0.4 接入。</p><p>不会读取、发送或记录 ChatGPT 网页内容。</p><p>当前模拟状态：{{labels[p.state]}}</p></div><div class="composer"><Plus :size="19"/> <span>只读远程占位 · 不发送消息</span></div></div></div>
          </div>
        </div>
        <div v-else class="empty">请选择一个项目</div>
      </div>
    </main>
    <aside class="right-panel">
      <div class="controls" aria-label="模拟任务控制"><button class="pause" :disabled="terminal||state==='PAUSED'||!selected" @click="act('pause')"><Pause :size="13"/> 暂停任务</button><button class="resume" :disabled="state!=='PAUSED'" @click="act('resume')"><Play :size="13"/> 恢复运行</button><button class="takeover" :disabled="!selected||terminal" :aria-pressed="takeover" @click="takeover=!takeover;log(takeover?'模拟接管开启':'模拟接管结束')"><User :size="13"/> {{takeover?'退出接管':'人工接管'}}</button><button class="stop" :disabled="terminal||!selected" @click="act('stop')"><Square :size="13"/> 结束任务</button></div>
      <template v-if="selected">
        <section><h3><PanelsTopLeft/> 项目信息</h3><b><GitHubMark :size="16"/> {{selected.name}}</b><dl><dt>仓库：</dt><dd><a v-if="repoURL" :href="repoURL" target="_blank" rel="noopener noreferrer">{{selected.repository}} <ExternalLink :size="11"/></a><span v-else>未配置</span></dd><dt>分支：</dt><dd>{{demoProject?'main（示例）':'未连接'}}</dd><dt>最近提交：</dt><dd><template v-if="demoProject"><code class="commit-chip">示例 SHA</code> <span class="subtle">10 分钟前</span></template><template v-else>未知</template></dd></dl></section>
        <section><h3><GitHubMark :size="15"/> GitHub CI 状态 <button class="sample-label" @click="log('CI 面板为本地示例，不代表 GitHub 实际结果');showNotices=true">查看示例</button></h3><div class="ci-state" :class="{'ci-demo-passed':demoPassed}"><CheckCircle2 :size="30" :stroke-width="demoPassed?1.5:2"/> <div><b>{{demoPassed?'模拟 CI 通过':state==='WAIT_CI'?'模拟等待 CI':'未连接 GitHub'}}</b><small>{{demoPassed?'示例数据 · 非真实 CI':'真实结果待 V0.3 接入'}}</small></div></div><dl><dt>CI 流水线：</dt><dd class="ci-detail">{{demoPassed?'Build & Test（示例）':'未知'}}<CheckCircle2 v-if="demoPassed" :size="13"/></dd><dt><Clock :size="12"/> 运行时间：</dt><dd class="ci-detail">{{demoPassed?'3 分 28 秒（示例）':'未知'}}<CheckCircle2 v-if="demoPassed" :size="13"/></dd></dl></section>
        <section><h3><RefreshCw/> 开发进度（模拟）</h3><div class="wide-progress"><div class="track" role="progressbar" aria-label="模拟开发进度" :aria-valuenow="selected.progress" :aria-valuemin="0" :aria-valuemax="100"><div :style="{width:selected.progress+'%'}"></div></div><b>{{selected.progress}}%</b></div><p>阶段：{{demoProject?'V0.8（第 8 / 10 阶段，示例）':'未连接真实任务计划'}}</p><div class="summary"><span>已完成： <b class="green">{{demoProject?'7':'未知'}}</b></span><span>进行中： <b class="yellow">{{demoProject?'1':'未知'}}</b></span><span>剩余： {{demoProject?'2':'未知'}}</span></div></section>
        <section><h3><Radio/> 任务状态（模拟）</h3><dl class="task-details"><dt><ListTodo :size="13"/> 当前状态</dt><dd><span class="state-chip">{{state}} · {{labels[state]}}</span></dd><dt><ArrowRight :size="13"/> 下一步</dt><dd class="next-step-value" :title="demoProject&&state==='DEVELOPING'?'优化连接稳定性并提交测试（示例）':'等待用户确认'">{{demoProject&&state==='DEVELOPING'?'优化连接稳定性（示例）':'等待用户确认'}}</dd><dt><RefreshCw :size="13"/> 上下文</dt><dd>未知</dd><dt><Clock :size="13"/> 交接文档</dt><dd>未连接</dd></dl></section>
        <section><h3><ArrowRight/> 下一步任务（待确认）</h3><ol class="next-actions" :aria-label="demoProject?'本地示例任务（不会自动执行）':'待确认任务'"><li><Circle :size="12"/> {{demoProject?'1. 优化连接稳定性（示例）':'配置真实 GitHub 项目'}}</li><li><ArrowRight :size="12"/> {{demoProject?'2. 实现自动重连机制':'接入 CI 状态验证'}}</li><li><ArrowRight :size="12"/> {{demoProject?'3. 补充单元测试并验证 CI':'接入远程浏览器'}}</li><li><ArrowRight :size="12"/> {{demoProject?'4. 更新 progress.json 并提交':'配置任务交接'}}</li></ol></section>
      </template>
    </aside>
    <footer><span><i class="green footer-status-dot" aria-hidden="true"></i> 当前：{{selected?.name||'未选中'}} / 模拟模式</span><span class="footer-duration"><Clock :size="15"/><span class="footer-value" :title="demoProject?'运行时长：2小时17分钟（模拟示例）':'运行时长未知'">时长：{{demoProject?'2小时17分（示例）':'未知'}}</span></span><span><PanelsTopLeft :size="15"/> 窗口数：{{tabs.length}}</span><span><GitHubMark :size="15"/> 最近同步：模拟</span><span class="footer-spacer" aria-hidden="true"></span><span><Wifi :size="15"/> 网络：未知</span><span><Cloud :size="15"/> 本地预览</span><span><Circle :size="13"/> 不调用付费 AI API</span></footer>
    <div v-if="showNotices" class="notice"><h3>运行日志 <button @click="showNotices=false"><X :size="16"/></button></h3><p v-for="(line,i) in notices" :key="i">{{line}}</p></div>
  </div>
</template>
