<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { api, engineLabel, type DeployTask, type ImageInfo } from '../api'
import { copyText, randomPassword } from '../clip'
import { errText } from '../format'

// 部署是独立的一页，不是弹窗：四步向导误点遮罩就全丢了，
// 而且部署过程要显示进度，弹窗的尺寸也不够用。
const props = defineProps<{
  // 从镜像页/列表带过来的预选版本（镜像 tag）
  version?: string
  // 要部署的引擎：mysql / postgres。两套版本号会撞号，必须按 family 过滤
  engine?: string
}>()
const emit = defineEmits<{ done: [name: string, port: number, id: string]; cancel: [] }>()

/* ---------------- 镜像与 Docker ---------------- */

// 版本清单就是官方镜像 tag：本地有没有、当前架构能不能跑都由后端实时判断。
const images = ref<ImageInfo[]>([])
const dockerOk = ref(false)
const dockerNote = ref('')
const loadError = ref('')
const loading = ref(false)
const arch = ref('')
// 加速站来自后端配置（NIUDBM_DOCKER_MIRROR）；有它时默认地址就带上前缀，
// 让用户看到"到底会去哪个地址拉"，而不是以为地址是写死的
const dockerMirror = ref('')
// 用户手动改过地址后，切换版本不再覆盖他的输入
const imageTouched = ref(false)

const engine = computed(() => props.engine || 'mysql')
// 版本清单按引擎过滤：MySQL 的 8.4 与 PG 的 16 不能出现在同一个下拉里
const versions = computed(() => images.value.filter((img) => img.supports && img.family === engine.value))

const blockedReason = computed(() => {
  if (loadError.value) return `镜像清单没读到：${loadError.value}`
  if (!dockerOk.value) {
    return dockerNote.value ? `Docker 不可用：${dockerNote.value}` : '读不到 Docker，无法拉起容器。'
  }
  if (!versions.value.length) return `当前架构没有可部署的 ${engineLabel(engine.value)} 镜像。`
  return ''
})

async function loadImages(): Promise<void> {
  loading.value = true
  try {
    const list = await api.listImages()
    images.value = list.items
    dockerOk.value = list.dockerOk
    arch.value = list.arch
    dockerMirror.value = list.dockerMirror ?? ''
    loadError.value = ''
  } catch (e) {
    images.value = []
    loadError.value = errText(e)
  } finally {
    loading.value = false
  }
  // preflight 只用来补一句 Docker 的具体原因（没装/没启动/socket 权限）
  try {
    const p = await api.preflight()
    dockerNote.value = p.docker.note ?? ''
  } catch {
    dockerNote.value = ''
  }
}

/* ---------------- 表单 ---------------- */

const form = ref({
  name: '',
  engineVersion: '',
  // image 是真正拉取的地址：默认按清单+加速站拼好，用户可改（私有仓库/换加速站）
  image: '',
  rootPassword: '',
  cpuLimit: 0,
  memLimitMB: 0,
  storageKind: 'bind',
  storagePath: '',
  port: '',
})

const showRootPwd = ref(false)
const formError = ref('')
const copyHint = ref('')

const chosen = computed(() => versions.value.find((v) => v.tag === form.value.engineVersion))

const stepKeys = ['version', 'secret', 'storage', 'deploy'] as const
const stepLabels: Record<string, string> = {
  version: '版本',
  secret: 'root 密码',
  storage: '存储',
  deploy: '资源与部署',
}
const step = ref(1)
const currentStep = computed(() => stepKeys[Math.min(step.value, stepKeys.length) - 1] ?? 'version')
const lastStep = computed(() => step.value >= stepKeys.length)

// 预选版本来自镜像页；没有就用清单里的默认 LTS
watch(
  versions,
  (list) => {
    if (form.value.engineVersion || !list.length) return
    const want = props.version ? list.find((v) => v.tag === props.version) : undefined
    form.value.engineVersion = (want ?? list.find((v) => v.default) ?? list[0]).tag
  },
  { immediate: true },
)

const CPU_CHOICES = [
  { value: 0, label: '不限制' },
  { value: 0.5, label: '0.5 核' },
  { value: 1, label: '1 核' },
  { value: 2, label: '2 核' },
  { value: 4, label: '4 核' },
]
const MEM_CHOICES = [
  { value: 0, label: '不限制' },
  { value: 512, label: '512 MB' },
  { value: 1024, label: '1 GB' },
  { value: 2048, label: '2 GB' },
  { value: 4096, label: '4 GB' },
  { value: 8192, label: '8 GB' },
]

const portNumber = computed(() => {
  const raw = String(form.value.port ?? '').trim()
  return raw ? Number(raw) : 0
})

/**
 * 服务名校验，规则与后端 validateName 一致。
 *
 * 前端必须自己先拦一次：名字不合法只有后端知道时，用户要走到最后一步才看到错，
 * 中间三步白填——这正是"为什么到最后才提示名字不对"的由来。
 */
function nameIssue(v: string): string {
  const name = v.trim()
  if (!name) return '请填写服务名称'
  if ([...name].length > 32) return '服务名最多 32 个字符'
  if (name.includes('..')) return '服务名不能包含 ..'
  if (/[\u0000-\u001f]/.test(name)) return '服务名不能包含控制字符'
  if (/[\/:*?"<>|]/.test(name)) return '服务名不能包含 / \\ : * ? " < > |'
  return ''
}

const nameError = computed(() => nameIssue(form.value.name))

/** 镜像地址校验：必须是完整引用且带版本标签（否则会悄悄拉 latest）。 */
function imageIssue(v: string): string {
  const ref = v.trim()
  if (!ref) return '请填写镜像地址'
  if (!/^[A-Za-z0-9._\-/:@]+$/.test(ref)) return '镜像地址只能包含字母、数字与 . _ - / : @'
  const slash = ref.lastIndexOf('/')
  const colon = ref.lastIndexOf(':')
  if (ref.includes('@') || colon < 0 || colon < slash || colon === ref.length - 1) {
    return '镜像地址要带版本标签，例如 docker.m.daocloud.io/library/postgres:17'
  }
  return ''
}

const imageError = computed(() => imageIssue(form.value.image))

/** 清单里的"官方地址"，配上加速站前缀后作为默认值。 */
function defaultImageFor(name: string): string {
  const mirror = dockerMirror.value.replace(/\/+$/, '')
  return mirror ? `${mirror}/library/${name}` : name
}

const suggestedImage = computed(() => {
  const name = chosen.value?.name ?? ''
  return name ? defaultImageFor(name) : ''
})

// 版本变了就把地址跟着改（除非用户自己改过）
watch(suggestedImage, (next) => {
  if (next && !imageTouched.value) form.value.image = next
})

function resetImage(): void {
  if (!suggestedImage.value) return
  form.value.image = suggestedImage.value
  imageTouched.value = false
}

function stepError(key: string): string {
  if (key === 'version') {
    const bad = nameIssue(form.value.name)
    if (bad) return bad
    // 版本清单读不到时不再一刀切拦住：只要手上有可用地址，就允许继续
    if (!versions.value.length && !form.value.image.trim()) return blockedReason.value
    if (versions.value.length && !form.value.engineVersion) return '请选择镜像版本'
    const badImage = imageIssue(form.value.image)
    if (badImage) return badImage
    return ''
  }
  if (key === 'secret') {
    if (form.value.rootPassword && form.value.rootPassword.length < 8) return 'root 密码至少 8 位'
    return ''
  }
  if (key === 'storage') {
    if (form.value.storageKind === 'bind') {
      const p = form.value.storagePath.trim()
      // 留空表示用应用数据目录下的默认位置，不算错误
      if (p && !p.startsWith('/')) return '宿主机目录必须是绝对路径，留空则用默认目录'
    }
    return ''
  }
  if (key === 'deploy') {
    const raw = String(form.value.port ?? '').trim()
    if (raw) {
      const p = Number(raw)
      if (!Number.isInteger(p) || p < 1024 || p > 65535) return '端口只能是 1024 ~ 65535 的整数，留空自动分配'
    }
    return ''
  }
  return ''
}

function nextStep(): void {
  const err = stepError(currentStep.value)
  if (err) {
    formError.value = err
    ElMessage.warning(err)
    return
  }
  formError.value = ''
  if (!lastStep.value) step.value += 1
}

function prevStep(): void {
  formError.value = ''
  if (step.value > 1) step.value -= 1
}

async function copyPassword(): Promise<void> {
  if (!form.value.rootPassword) return
  try {
    await copyText(form.value.rootPassword)
    copyHint.value = 'root 密码已复制，请先保存好'
  } catch (e) {
    formError.value = errText(e)
  }
}

/* ---------------- 部署与进度 ---------------- */

interface PhaseItem {
  key: string
  text: string
}

const PHASES: PhaseItem[] = [
  { key: 'prepare', text: '检查版本与端口' },
  { key: 'pull', text: '拉取镜像' },
  { key: 'start', text: '启动并等待就绪' },
  { key: 'init', text: '初始化账号与系统库' },
  { key: 'done', text: '完成' },
]

const deploying = ref(false)
const task = ref<DeployTask | null>(null)
const deployError = ref('')
const elapsed = ref(0)
let pollTimer: number | undefined
let tickTimer: number | undefined

const phaseIndex = computed(() => {
  const p = task.value?.phase ?? 'prepare'
  // 后端的 finalize 是「容器已就绪、正在收尾」，界面上并入「完成」：
  // 不映射的话 findIndex 落空会退回 0，进度条会在最后一步倒回第一步。
  const key = p === 'finalize' ? 'done' : p
  const i = PHASES.findIndex((x) => x.key === key)
  return i < 0 ? 0 : i
})

function stopTimers(): void {
  if (pollTimer !== undefined) window.clearTimeout(pollTimer)
  if (tickTimer !== undefined) window.clearInterval(tickTimer)
  pollTimer = undefined
  tickTimer = undefined
}

async function pollTask(taskId: string): Promise<void> {
  try {
    const t = await api.deployTask(taskId)
    task.value = t
    if (t.status === 'running') {
      pollTimer = window.setTimeout(() => void pollTask(taskId), 800)
      return
    }
    stopTimers()
    if (t.status === 'failed') {
      deployError.value = t.error || '部署失败，原因未知'
      deploying.value = false
      return
    }
    // 成功：把结果交给上层，由它切回列表
    emit('done', t.name, portNumber.value, t.instanceId ?? '')
  } catch (e) {
    stopTimers()
    deployError.value = errText(e)
    deploying.value = false
  }
}

async function submit(): Promise<void> {
  if (deploying.value) return
  const err = stepError('version') || stepError('secret') || stepError('storage') || stepError('deploy')
  if (err) {
    formError.value = err
    return
  }
  formError.value = ''
  deployError.value = ''
  deploying.value = true
  elapsed.value = 0
  task.value = null
  tickTimer = window.setInterval(() => {
    elapsed.value += 1
  }, 1000)

  const port = portNumber.value
  try {
    const res = await api.createInstance({
      name: form.value.name.trim(),
      engine: engine.value,
      engineVersion: form.value.engineVersion,
      // 地址随请求一起发：后端按它拉镜像，并记进台账供重建容器时沿用
      image: form.value.image.trim(),
      profile: 'standard',
      rootPassword: form.value.rootPassword || undefined,
      cpuLimit: form.value.cpuLimit || undefined,
      memLimitMB: form.value.memLimitMB || undefined,
      storageKind: form.value.storageKind,
      storagePath: form.value.storageKind === 'bind' ? form.value.storagePath.trim() : undefined,
      port: port > 0 ? port : undefined,
    })
    if ('taskId' in res) {
      await pollTask(res.taskId)
      return
    }
    // 理论上部署都走异步；真同步返回就直接当成功
    stopTimers()
    emit('done', res.name, res.port ?? 0, res.id)
  } catch (e) {
    stopTimers()
    // 请求在拿到任务号之前就失败（重名、Docker 不可用、版本不支持、会话过期…）：
    // 这时进度卡不会出现，必须把原因留在表单上并弹一次提示，
    // 否则界面表现就是"点一下、闪一下、什么都没发生"。
    const msg = errText(e)
    // 最常见的两种"点不动"：名字被上次失败的部署占了、Docker 根本没就绪。
    // 都直接告诉用户下一步做什么，而不是只报一句错。
    deployError.value = msg.includes('已存在')
      ? `${msg}（通常是上次部署失败留下的记录：在列表里删掉那条，或换个名字重试）`
      : msg
    formError.value = deployError.value
    deploying.value = false
    task.value = null
    ElMessage.error(deployError.value)
  }
}

/* ---------------- 部署失败现场 ---------------- */

// 失败时后端保留了容器与数据目录，容器日志固化在实例目录里。
// 这里是用户唯一能看到"到底为什么没起来"的入口。
const logOpen = ref(false)
const logLoading = ref(false)
const logText = ref('')
const logPath = ref('')
const logError = ref('')

async function viewDeployLog(): Promise<void> {
  const id = task.value?.instanceId
  if (!id) {
    logError.value = '这个任务没有留下实例记录（可能是参数校验阶段就失败了），操作记录里有原因。'
    logOpen.value = true
    return
  }
  logOpen.value = true
  logLoading.value = true
  logText.value = ''
  logError.value = ''
  try {
    const d = await api.deployLog(id)
    logText.value = d.log
    logPath.value = d.path
    if (!d.available) logError.value = d.message ?? '还没有部署日志'
  } catch (e) {
    logError.value = errText(e)
  } finally {
    logLoading.value = false
  }
}

// 失败记录会留在列表里（便于看日志），同名再部署会撞名字。
// 所以"修改后重试"先把这条失败实例删掉，再回到表单重新填。
/** 拉取失败后：改地址 → 清掉失败现场 → 用新地址再部署一次。 */
async function retryWithImage(): Promise<void> {
  const bad = imageIssue(form.value.image)
  if (bad) {
    ElMessage.warning(bad)
    return
  }
  imageTouched.value = true
  await retry()
  if (deployError.value) return
  await submit()
}

async function retry(): Promise<void> {
  const id = task.value?.instanceId
  if (id) {
    try {
      await api.deleteInstance(id, form.value.name.trim(), false)
    } catch (e) {
      deployError.value = `清理失败现场时出错：${errText(e)}`
      return
    }
  }
  deployError.value = ''
  deploying.value = false
  task.value = null
  logOpen.value = false
  logText.value = ''
}

onMounted(async () => {
  await loadImages()
  if (!form.value.rootPassword) form.value.rootPassword = randomPassword()
})

onBeforeUnmount(stopTimers)
</script>

<template>
  <div class="deploy">
    <div class="deploy-head">
      <button type="button" class="btn btn-sm" :disabled="deploying" @click="emit('cancel')">← 返回列表</button>
      <h2>部署 {{ engineLabel(engine) }} 服务</h2>
      <span class="muted small">一个服务占一个端口；服务建好后，进去再建数据库。</span>
    </div>

    <!-- 部署中：只看进度，不给改动机会 -->
    <section v-if="deploying || task" class="card progress-card">
      <div class="row">
        <strong v-if="!deployError">正在部署 {{ form.name }}…</strong>
        <strong v-else class="bad-text">部署失败</strong>
        <span class="muted small">已用 {{ elapsed }} 秒</span>
      </div>

      <ol class="phases">
        <li
          v-for="(p, i) in PHASES"
          :key="p.key"
          :class="{
            done: task?.status === 'ok' ? i <= phaseIndex : i < phaseIndex,
            active: task?.status === 'running' && i === phaseIndex,
            failed: task?.status === 'failed' && i === phaseIndex,
          }"
        >
          <span class="phase-dot" />
          <span class="phase-text">{{ p.text }}</span>
          <span v-if="task?.status === 'running' && i === phaseIndex" class="muted small">
            {{ task.phaseText }}
          </span>
        </li>
      </ol>

      <p class="muted small">
        首次部署要拉取镜像，NAS 上可能要几分钟；这段时间可以离开页面，服务建好后会出现在列表里。
      </p>

      <div v-if="deployError" class="notice notice-bad">
        <span>{{ deployError }}</span>
      </div>

      <!-- 拉取失败最常见的原因就是地址连不上：就地改，别再回第一步翻 -->
      <div v-if="deployError" class="field">
        <label for="d-image-retry">换个镜像地址再试</label>
        <div class="row">
          <input
            id="d-image-retry"
            v-model="form.image"
            class="input mono"
            spellcheck="false"
            placeholder="docker.m.daocloud.io/library/postgres:17"
            @input="imageTouched = true"
          />
          <button type="button" class="btn" :disabled="deploying" @click="retryWithImage">用这个地址重试</button>
        </div>
        <span class="field-hint">
          会先删掉上面这条失败记录（不含数据目录），再用新地址部署一次。地址必须带版本标签。
        </span>
      </div>

      <p v-if="deployError" class="muted small">
        失败现场已保留：容器与数据目录没有被删除，列表里也会留下一条「部署失败」记录。
        点「查看部署日志」能看到当时的容器输出，用来判断是权限、端口还是初始化太慢。
      </p>

      <template v-if="logOpen">
        <div v-if="logPath" class="log-path muted small">{{ logPath }}</div>
        <pre v-if="logText" class="log">{{ logText }}</pre>
        <div v-if="logLoading" class="muted small">读取日志中…</div>
        <div v-if="logError" class="notice notice-bad">{{ logError }}</div>
      </template>

      <div class="row">
        <template v-if="deployError">
          <button type="button" class="btn" @click="viewDeployLog">查看部署日志</button>
          <button type="button" class="btn" @click="retry">修改后重试</button>
          <button type="button" class="btn" @click="emit('cancel')">返回列表</button>
        </template>
        <template v-else-if="deploying">
          <button type="button" class="btn" @click="emit('cancel')">后台继续（回列表）</button>
        </template>
      </div>
    </section>

    <template v-else>
      <ol class="steps" aria-label="部署步骤">
        <li
          v-for="(k, i) in stepKeys"
          :key="k"
          :class="{ active: currentStep === k, done: step > i + 1 }"
        >
          <span class="step-no">{{ step > i + 1 ? '✓' : i + 1 }}</span>
          <span class="step-name">{{ stepLabels[k] }}</span>
        </li>
      </ol>

      <section v-if="!versions.length" class="notice notice-bad diag">
        <div class="diag-main">
          <strong>现在选不到版本</strong>
          <span>{{ blockedReason || '正在读取镜像清单…' }}</span>
        </div>
        <div class="row">
          <button type="button" class="btn btn-sm" :disabled="loading" @click="loadImages">
            {{ loading ? '读取中…' : '重新读取' }}
          </button>
        </div>
      </section>

      <section class="card">
        <!-- 第一步：版本 -->
        <template v-if="currentStep === 'version'">
          <div class="field">
            <label for="d-name">服务名称</label>
            <input
              id="d-name"
              v-model="form.name"
              class="input"
              maxlength="32"
              :placeholder="`例如 ${engine === 'postgres' ? 'pg' : 'mysql'}-业务`"
            />
            <span v-if="nameError && form.name" class="field-hint bad-text">{{ nameError }}</span>
            <span v-else class="field-hint">
              最多 32 个字符，不能包含 / \ : * ? " &lt; &gt; |；点「下一步」时就会校验，不用等到最后。
            </span>
          </div>

          <div class="field">
            <label for="d-version">镜像版本</label>
            <select id="d-version" v-model="form.engineVersion" class="select">
              <option v-for="v in versions" :key="v.tag" :value="v.tag">
                {{ v.label }} · {{ v.name }}<template v-if="v.local"> · 本机已有</template>
              </option>
            </select>
            <span class="field-hint">
              版本决定 SQL 兼容性，建好后不能原地换版本；换版本请部署新的服务再迁移数据。
              <template v-if="chosen?.note"> {{ chosen.note }}。</template>
              <template v-if="arch"> 当前架构 {{ arch }}。</template>
            </span>
          </div>

          <!-- 镜像地址：国内网络常常拉不动 Docker Hub，必须让用户自己改 -->
          <div class="field">
            <label for="d-image">镜像地址</label>
            <div class="row">
              <input
                id="d-image"
                v-model="form.image"
                class="input mono"
                spellcheck="false"
                :placeholder="suggestedImage || 'docker.m.daocloud.io/library/postgres:17'"
                @input="imageTouched = true"
              />
              <button
                v-if="suggestedImage && form.image !== suggestedImage"
                type="button"
                class="btn btn-sm"
                @click="resetImage"
              >
                恢复默认
              </button>
            </div>
            <span v-if="imageError && form.image" class="field-hint bad-text">{{ imageError }}</span>
            <span v-else class="field-hint">
              实际就拉这个地址，必须带版本标签。
              <template v-if="dockerMirror">已按加速站 <span class="mono">{{ dockerMirror }}</span> 补全；</template>
              拉不动时可以换成自己的加速站或私有仓库，例如
              <span class="mono">docker.m.daocloud.io/library/{{ chosen?.name || 'postgres:17' }}</span>。
            </span>
          </div>
        </template>

        <!-- 第二步：root 密码 -->
        <template v-else-if="currentStep === 'secret'">
          <div class="field">
            <label for="d-root">root 密码</label>
            <div class="row">
              <input
                id="d-root"
                v-model="form.rootPassword"
                class="input mono"
                :type="showRootPwd ? 'text' : 'password'"
                placeholder="至少 8 位"
                autocomplete="new-password"
              />
              <button type="button" class="btn btn-sm" @click="showRootPwd = !showRootPwd">
                {{ showRootPwd ? '隐藏' : '显示' }}
              </button>
              <button type="button" class="btn btn-sm" @click="form.rootPassword = randomPassword()">随机</button>
              <button type="button" class="btn btn-sm" @click="copyPassword">复制</button>
            </div>
            <span class="field-hint">只用于容器初始化；应用连接请用服务里建的数据库账号。密码仅此处可见。</span>
          </div>
        </template>

        <!-- 第三步：存储 -->
        <template v-else-if="currentStep === 'storage'">
          <div class="field">
            <label>数据存放位置</label>
            <div class="choice-list">
              <label class="choice" :class="{ active: form.storageKind === 'bind' }">
                <input v-model="form.storageKind" type="radio" value="bind" />
                <span>
                  <strong>宿主机目录</strong>
                  <em>推荐。文件看得见、算得出占用，方便单独备份；留空用应用数据目录。</em>
                </span>
              </label>
              <label class="choice" :class="{ active: form.storageKind === 'volume' }">
                <input v-model="form.storageKind" type="radio" value="volume" />
                <span>
                  <strong>Docker 命名卷</strong>
                  <em>Docker 管属主与权限，路径不可见，列表里体积会显示"卷内"。</em>
                </span>
              </label>
            </div>
          </div>
          <div v-if="form.storageKind === 'bind'" class="field">
            <label for="d-storage">宿主机目录</label>
            <input id="d-storage" v-model="form.storagePath" class="input mono" placeholder="留空使用应用数据目录；也可填授权共享目录下的子目录" />
            <span class="field-hint">只允许飞牛授权目录或应用私有数据目录下的子目录，避免误挂系统目录。</span>
          </div>
        </template>

        <!-- 第四步：资源与端口 -->
        <template v-else>
          <div class="field">
            <label for="d-port">端口</label>
            <input
              id="d-port"
              v-model="form.port"
              class="input mono"
              inputmode="numeric"
              placeholder="留空自动分配（从 33000 起）"
            />
            <span class="field-hint">
              一个服务一个端口。容器在 Docker 里的名字就是
              <code>{{ engine }}-{{ form.engineVersion || '版本' }}-{{ portNumber || '端口' }}</code>。
            </span>
          </div>

          <div class="field">
            <label for="d-cpu">CPU 上限</label>
            <select id="d-cpu" v-model.number="form.cpuLimit" class="select">
              <option v-for="c in CPU_CHOICES" :key="c.value" :value="c.value">{{ c.label }}</option>
            </select>
          </div>
          <div class="field">
            <label for="d-mem">内存上限</label>
            <select id="d-mem" v-model.number="form.memLimitMB" class="select">
              <option v-for="m in MEM_CHOICES" :key="m.value" :value="m.value">{{ m.label }}</option>
            </select>
            <span class="field-hint">{{ engineLabel(engine) }} 官方镜像建议至少 512MB；超限会被 Docker OOM 杀掉。</span>
          </div>

          <dl class="summary">
            <div><dt>服务名</dt><dd>{{ form.name || '—' }}</dd></div>
            <div><dt>版本</dt><dd>{{ form.engineVersion || '默认' }}</dd></div>
            <div class="wide"><dt>镜像</dt><dd class="mono">{{ form.image || '—' }}</dd></div>
            <div><dt>运行方式</dt><dd>Docker 容器</dd></div>
            <div><dt>端口</dt><dd>{{ portNumber || '自动分配' }}</dd></div>
          </dl>
          <p class="muted small">部署完成后是一个空服务，数据库要进去之后再建。</p>
        </template>

        <div v-if="copyHint" class="notice notice-ok">{{ copyHint }}</div>
        <div v-if="formError" class="notice notice-bad">{{ formError }}</div>
        <div v-else-if="deployError" class="notice notice-bad">{{ deployError }}</div>
      </section>

      <div class="deploy-foot">
        <button type="button" class="btn" @click="emit('cancel')">取消</button>
        <button v-if="step > 1" type="button" class="btn" @click="prevStep">上一步</button>
        <button v-if="!lastStep" type="button" class="btn btn-primary" @click="nextStep">下一步</button>
        <button v-else type="button" class="btn btn-primary" :disabled="deploying" @click="submit">
          {{ deploying ? '部署中…' : '部署服务' }}
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.deploy {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 760px;
}

.deploy-head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  flex-wrap: wrap;
}

.deploy-head h2 {
  margin: 0;
}

.deploy-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.steps {
  display: flex;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
  flex-wrap: wrap;
}

.steps li {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px 4px 4px;
  border: 1px solid var(--line);
  border-radius: 999px;
  color: var(--fg-muted);
  font-size: 13px;
}

.steps li.active {
  border-color: var(--accent);
  color: var(--accent);
  background: var(--accent-soft);
}

.steps li.done {
  color: var(--ok);
  border-color: var(--ok);
}

.step-no {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--bg);
  font-size: 12px;
}

.choice-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.choice {
  display: flex;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--line);
  border-radius: 10px;
  cursor: pointer;
}

.choice.active {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.choice span {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.choice em {
  color: var(--fg-muted);
  font-style: normal;
  font-size: 13px;
}

.summary {
  display: grid;
  gap: 6px;
  margin: 14px 0 0;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--bg-elevated);
  font-size: 13.5px;
}

.summary div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.summary dt {
  color: var(--fg-muted);
}

/* 镜像地址较长，独占一行并允许折行，避免把汇总撑破 */
.summary .wide {
  display: block;
}

.summary .wide dd {
  margin: 2px 0 0;
  word-break: break-all;
}

.summary dd {
  margin: 0;
  text-align: right;
  word-break: break-all;
}

.progress-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 部署现场：失败日志用等宽字体原样显示，长行横向换行而不是撑破卡片 */
.log-path {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  word-break: break-all;
}

.log {
  max-height: 46vh;
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg);
  color: var(--fg);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-all;
}

.phases {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.phases li {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--fg-muted);
  font-size: 13.5px;
}

.phase-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 2px solid var(--line);
  flex: none;
}

.phases li.active {
  color: var(--accent);
}

.phases li.active .phase-dot {
  border-color: var(--accent);
  background: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.phases li.done {
  color: var(--ok);
}

.phases li.done .phase-dot {
  border-color: var(--ok);
  background: var(--ok);
}

.phases li.failed {
  color: var(--bad);
}

.phases li.failed .phase-dot {
  border-color: var(--bad);
  background: var(--bad);
}

.diag {
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 10px;
}

.diag-main {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1 1 240px;
  text-align: left;
}

.bad-text {
  color: var(--bad);
}
</style>
