<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api'
import type { ConnectionTest } from '../api'
import type { Target } from '../types'

const targets = ref<Target[]>([])
const showForm = ref(false)
const editingId = ref('')
const editingEnabled = ref(true)
const loading = ref(true)
const error = ref('')
const draftTest = ref<ConnectionTest | null>(null)
const testingDraft = ref(false)
const testingTarget = ref('')
const deletingTarget = ref('')
const savedTests = ref<Record<string, ConnectionTest>>({})
const form = ref(emptyForm())

function targetPayload() { return { ...form.value, private_key_path:form.value.auth_method==='key'?form.value.private_key_path:undefined, password:form.value.auth_method==='password'?form.value.password:undefined } }

async function load() { loading.value=true;try{targets.value=(await api.targets()).items??[]}catch(cause){error.value=cause instanceof Error?cause.message:'加载失败'}finally{loading.value=false} }
async function saveTarget() {
  error.value=''
  try {
    if(editingId.value){await api.updateTarget(editingId.value,{...targetPayload(),enabled:editingEnabled.value})}
    else{await api.createTarget({...targetPayload(),enabled:true})}
    closeForm();await load()
  }
  catch(cause){error.value=cause instanceof Error?cause.message:'保存失败'}
}
function emptyForm(){return{name:'',host:'',port:22,ssh_user:'',auth_method:'key',private_key_path:'',password:'',description:'',host_key_fingerprint:''}}
function openCreate(){editingId.value='';editingEnabled.value=true;form.value=emptyForm();draftTest.value=null;error.value='';showForm.value=true}
function openEdit(target:Target){editingId.value=target.id;editingEnabled.value=target.enabled;form.value={name:target.name,host:target.host,port:target.port,ssh_user:target.ssh_user,auth_method:target.auth_method||'key',private_key_path:target.private_key_path||'',password:'',description:target.description,host_key_fingerprint:target.host_key_fingerprint};draftTest.value=null;error.value='';showForm.value=true}
function closeForm(){showForm.value=false;editingId.value='';editingEnabled.value=true;form.value=emptyForm();draftTest.value=null;error.value=''}
async function testDraftTarget() {
  error.value='';draftTest.value=null;testingDraft.value=true
  try { draftTest.value=await api.testTarget({ ...targetPayload(), existing_target_id:editingId.value||undefined, enabled:true }) }
  catch(cause){error.value=cause instanceof Error?cause.message:'测试失败'}
  finally{testingDraft.value=false}
}
async function testSavedTarget(id:string) {
  testingTarget.value=id
  try { savedTests.value[id]=await api.testSavedTarget(id) }
  catch(cause){savedTests.value[id]={success:false,duration_ms:0,message:cause instanceof Error?cause.message:'测试失败'}}
  finally{testingTarget.value=''}
}
async function deleteTarget(target:Target) {
  if (!window.confirm(`确定删除目标主机“${target.name}”吗？删除后无法在页面恢复，但历史执行记录会保留。`)) return
  error.value='';deletingTarget.value=target.id
  try { await api.deleteTarget(target.id);targets.value=targets.value.filter(item=>item.id!==target.id);delete savedTests.value[target.id] }
  catch(cause){error.value=cause instanceof Error?cause.message:'删除失败'}
  finally{deletingTarget.value=''}
}
onMounted(load)
</script>

<template>
  <section class="page-intro"><div><h2>目标主机</h2><p>配置允许 AI Agent 通过 SSH Bridge 访问的主机</p></div><button class="primary-button" @click="openCreate"><span>＋</span> 添加主机</button></section>
  <p v-if="error && !showForm" class="form-error panel">{{ error }}</p>
  <div v-if="loading" class="panel empty-state">正在加载…</div>
  <div v-else-if="!targets.length" class="panel empty-state">尚未添加目标主机</div>
  <section v-else class="target-grid">
    <article v-for="target in targets" :key="target.id" class="target-card">
      <div class="target-top"><div class="server-icon">▤</div><div class="target-title"><h3>{{ target.name }}</h3><p>{{ target.description || '未填写说明' }}</p></div><span class="host-status unknown"><i></i>{{ target.enabled ? '已启用' : '已停用' }}</span></div>
      <dl class="target-meta"><div><dt>连接地址</dt><dd>{{ target.host }}:{{ target.port }}</dd></div><div><dt>SSH 用户</dt><dd>{{ target.ssh_user }}</dd></div><div><dt>认证方式</dt><dd>{{ target.auth_method==='password'?'密码':'私钥' }}</dd></div><div><dt>Host Key</dt><dd>{{ target.host_key_fingerprint || '未校验' }}</dd></div></dl>
      <div v-if="savedTests[target.id]" class="connection-result" :class="{ success:savedTests[target.id].success, failure:!savedTests[target.id].success }"><strong>{{ savedTests[target.id].success?'SSH 登录成功':'SSH 登录失败' }}</strong><span>{{ savedTests[target.id].message }} · {{ savedTests[target.id].duration_ms }} ms</span></div>
      <div class="target-actions"><button @click="testSavedTarget(target.id)" :disabled="testingTarget===target.id||deletingTarget===target.id">{{ testingTarget===target.id?'正在测试…':'测试 SSH 登录' }}</button><button @click="openEdit(target)" :disabled="deletingTarget===target.id">编辑</button><button class="danger-button" :disabled="deletingTarget===target.id" @click="deleteTarget(target)">{{ deletingTarget===target.id?'正在删除…':'删除' }}</button></div>
    </article>
  </section>
  <div v-if="showForm" class="modal-layer" @click.self="closeForm">
    <form class="modal" @submit.prevent="saveTarget">
      <div class="modal-heading"><div><h2>{{ editingId?'编辑目标主机':'添加目标主机' }}</h2><p>{{ editingId?'修改连接配置后保存':'主机保存后可供 Agent 发起远程执行' }}</p></div><button type="button" @click="closeForm">×</button></div>
      <div class="form-grid">
        <label class="full"><span>主机名称</span><input v-model="form.name" placeholder="例如 prod-web-01" required /></label>
        <label><span>主机地址</span><input v-model="form.host" placeholder="10.0.1.20" required /></label><label><span>SSH 端口</span><input v-model.number="form.port" type="number" required /></label>
        <label class="full"><span>SSH 用户</span><input v-model="form.ssh_user" placeholder="ai-operator" required /></label>
        <label class="full"><span>认证方式</span><select v-model="form.auth_method"><option value="key">私钥</option><option value="password">用户名 + 密码</option></select></label>
        <label v-if="form.auth_method==='key'" class="full"><span>服务端私钥路径</span><input v-model="form.private_key_path" placeholder="/Users/me/.ssh/id_ed25519" required /></label>
        <label v-else class="full"><span>SSH 密码</span><input v-model="form.password" type="password" autocomplete="new-password" :required="!editingId || !targets.find(t=>t.id===editingId)?.password_configured" :placeholder="editingId?'留空则保留原密码':'输入 SSH 密码'" /><small v-if="editingId">留空则保留原密码；密码不会在页面中回显。</small></label>
        <label class="full"><span>主机说明</span><input v-model="form.description" placeholder="这台主机的用途" /></label>
        <label class="full"><span>Host Key（可选）</span><textarea v-model="form.host_key_fingerprint" placeholder="可直接粘贴 ~/.ssh/known_hosts 中对应主机的完整一行"></textarea><small>留空时不校验；填写后支持 known_hosts 完整记录、公钥或 SHA256 指纹，并在连接时严格校验。</small></label>
      </div>
      <div v-if="draftTest" class="connection-result" :class="{ success:draftTest.success, failure:!draftTest.success }"><strong>{{ draftTest.success?'SSH 登录成功':'SSH 登录失败' }}</strong><span>{{ draftTest.message }} · {{ draftTest.duration_ms }} ms</span></div>
      <p v-if="error" class="form-error">{{ error }}</p>
      <div class="modal-actions"><button type="button" class="secondary-button" :disabled="testingDraft" @click="testDraftTarget">{{ testingDraft?'正在测试…':'测试 SSH 登录' }}</button><span class="modal-actions-spacer"></span><button type="button" class="secondary-button" @click="closeForm">取消</button><button class="primary-button">{{ editingId?'保存修改':'保存主机' }}</button></div>
    </form>
  </div>
</template>
