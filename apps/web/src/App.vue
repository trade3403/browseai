<script setup lang="ts">
import {computed, onMounted, ref} from 'vue'
import {Cloud, Home, ListTodo, PanelsTopLeft, Github, ScrollText, Settings, Plus, Bell, X, Monitor, Keyboard, Mouse, RefreshCw, Maximize, Columns2, Pause, Play, User, Square, CheckCircle2, Circle, ArrowRight, Radio, Wifi, Clock, ExternalLink, ChevronDown} from 'lucide-vue-next'
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
      <div class="brand"><Cloud :size="30"/><strong>AI Browser 云端开发管理平台</strong><span class="version">v0.1.0</span></div>
      <span class="tagline">基于 ChatGPT 网页的开发助手 · 云端运行 · 本地实时查看 · 不调用付费 AI API</span>
      <div class="header-actions"><span class="online"><Circle :size="10" fill="currentColor"/> 模拟模式</span><button title="通知" @click="showNotices=!showNotices"><Bell :size="19"/></button><button title="设置" @click="activeNav='系统设置'"><Settings :size="19"/></button><span class="user-avatar"><User :size="16"/></span><span>本地用户</span><ChevronDown :size="12"/></div>
    </header>
    <aside class="sidebar">
      <nav><button v-for="item in nav" :key="item.name" :class="{active:activeNav===item.name}" @click="activeNav=item.name"><component :is="item.icon" :size="18"/>{{item.name}}</button></nav>
      <div class="section-heading">项目列表 ({{projects.length}})<button title="打开下一个项目" :disabled="!projects.length" @click="nextWindow"><Plus :size="17"/></button></div>
      <div class="project-list">
        <button v-for="p in projects" :key="p.id" class="project" :class="{selected:p.id===current}" @click="open(p.id);activeNav='项目总览'">
          <div class="project-head"><Github :size="19"/><b>{{p.name}}</b><span :class="statusColor(p.state)">● {{labels[p.state]}}</span></div>
          <div class="project-progress"><div class="track"><div :style="{width:p.progress+'%'}" :class="{complete:p.state==='COMPLETED'}"></div></div><span>{{p.progress}}%</span></div>
        </button>
      </div>
    </aside>
    <main class="workspace">
      <div class="workspace-toolbar">
        <strong><PanelsTopLeft :size="19"/> 远程浏览器窗口</strong>
        <div class="tabs">
          <div v-for="p in tabProjects" :key="p.id" class="tab" :class="{active:p.id===current}"><button @click="open(p.id);activeNav='项目总览'"><i :class="statusColor(p.state)">●</i> {{p.name}}</button><button :aria-label="'关闭 '+p.name" @click="close(p.id)"><X :size="13"/></button></div>
          <button aria-label="新增标签" @click="nextWindow"><Plus :size="18"/></button>
        </div>
        <div class="toolbar-buttons"><button :aria-pressed="split" @click="split=!split"><Columns2 :size="15"/> 分屏</button><button :aria-pressed="expanded" @click="expanded=!expanded"><Maximize :size="15"/> {{expanded?'退出全屏':'全屏'}}</button><button @click="nextWindow"><Plus :size="14"/> 新建窗口</button></div>
      </div>
      <div class="remote">
        <div class="remote-status"><span><i class="green">●</i> 云端 Chrome（模拟） · 分辨率 1920×1080 · 延迟未知</span><div><span class="live"><Monitor :size="13"/> 模拟画面</span><button disabled><Keyboard :size="13"/> 键盘</button><button disabled><Mouse :size="13"/> 鼠标</button><button @click="load"><RefreshCw :size="13"/> 刷新</button></div></div>
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
      <div class="controls"><button class="pause" :disabled="terminal||state==='PAUSED'||!selected" @click="act('pause')"><Pause :size="13"/> 暂停任务</button><button class="resume" :disabled="state!=='PAUSED'" @click="act('resume')"><Play :size="13"/> 恢复运行</button><button class="takeover" :disabled="!selected||terminal" :aria-pressed="takeover" @click="takeover=!takeover;log(takeover?'模拟接管开启':'模拟接管结束')"><User :size="13"/> {{takeover?'退出接管':'人工接管'}}</button><button class="stop" :disabled="terminal||!selected" @click="act('stop')"><Square :size="13"/> 结束任务</button></div>
      <template v-if="selected">
        <section><h3><PanelsTopLeft/> 项目信息</h3><b><Github :size="16"/> {{selected.name}}</b><dl><dt>仓库：</dt><dd>{{selected.repository||'未配置'}} <ExternalLink v-if="selected.repository" :size="11"/></dd><dt>分支：</dt><dd>未连接</dd><dt>最近提交：</dt><dd>未知</dd></dl></section>
        <section><h3><Github/> GitHub CI 状态</h3><div class="ci-state"><CheckCircle2 :size="30"/> <div><b>{{state==='WAIT_CI'?'模拟等待 CI':'未连接 GitHub'}}</b><small>真实结果待 V0.3 接入</small></div></div><dl><dt>CI 流水线：</dt><dd>未知</dd><dt>运行时间：</dt><dd>未知</dd></dl></section>
        <section><h3><RefreshCw/> 开发进度（模拟）</h3><div class="wide-progress"><div class="track"><div :style="{width:selected.progress+'%'}"></div></div><b>{{selected.progress}}%</b></div><p>阶段：未连接真实任务计划</p><div class="summary"><span>已完成：未知</span><span>进行中：未知</span></div></section>
        <section><h3><Radio/> 任务状态（模拟）</h3><dl><dt>当前状态</dt><dd><span class="state-chip">{{state}} · {{labels[state]}}</span></dd><dt>下一步</dt><dd>等待用户确认</dd><dt>上下文</dt><dd>未知</dd><dt>交接文档</dt><dd>未连接</dd></dl></section>
        <section><h3><ArrowRight/> 下一步任务</h3><ol><li>配置真实 GitHub 项目</li><li>接入 CI 状态验证</li><li>接入远程浏览器</li><li>配置任务交接</li></ol></section>
      </template>
    </aside>
    <footer><span><i class="green">●</i> 当前：{{selected?.name||'未选中'}} / 模拟模式</span><span><Clock :size="15"/> 运行时长：未知</span><span><PanelsTopLeft :size="15"/> 窗口：{{tabs.length}}</span><span><Github :size="15"/> 最近同步：模拟</span><span class="footer-spacer"></span><span><Wifi :size="15"/> 网络：未知</span><span><Cloud :size="15"/> 本地预览</span><span><Circle :size="13"/> 不调用付费 AI API</span></footer>
    <div v-if="showNotices" class="notice"><h3>运行日志 <button @click="showNotices=false"><X :size="16"/></button></h3><p v-for="(line,i) in notices" :key="i">{{line}}</p></div>
  </div>
</template>
