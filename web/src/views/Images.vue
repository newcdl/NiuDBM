<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, engineLabel, type ImageList, type ImageInfo } from '../api'
import { errText } from '../format'
import EngineTabs from '../components/EngineTabs.vue'
import type { Engine } from '../engine'

const engine = ref<Engine>('mysql')

const list = ref<ImageList | null>(null)
const loading = ref(false)
const error = ref('')

// 拉取是异步任务：按 tag 记住 jobId 与进度，轮询到终态再刷新列表
const pulling = ref<Record<string, { jobId: string; progress: number }>>({})
const timers: Record<string, number> = {}

const items = computed<ImageInfo[]>(() => list.value?.items ?? [])

// 引擎页签各管各的：MySQL 页签下是 MySQL + MariaDB（同一套协议，
// 但版本语义不同所以要分两段），PostgreSQL 页签下只有 PG。
// 三个引擎的版本号会撞（都叫 16），混在一页里最容易被误选。
const FAMILIES_OF_TAB: Record<Engine, string[]> = {
  mysql: ['mysql', 'mariadb'],
  postgresql: ['postgres'],
  mongodb: ['mongodb'],
}

const visible = computed(() =>
  items.value.filter((i) => FAMILIES_OF_TAB[engine.value].includes(i.family)),
)

const groups = computed(() => {
  const order: { family: string; title: string }[] = [
    { family: 'mysql', title: 'MySQL' },
    { family: 'mariadb', title: 'MariaDB' },
    { family: 'postgres', title: 'PostgreSQL' },
    { family: 'mongodb', title: 'MongoDB' },
  ]
  return order
    .map((g) => {
      const list = visible.value.filter((i) => i.family === g.family)
      return { ...g, items: list, ready: list.filter((i) => i.local).length }
    })
    .filter((g) => g.items.length)
})

const stats = computed(() => {
  const all = visible.value
  const inUse = new Set<string>()
  all.forEach((i) => (i.usedBy ?? []).forEach((n) => inUse.add(n)))
  return {
    total: all.length,
    local: all.filter((i) => i.local).length,
    used: inUse.size,
    arch: list.value?.arch ?? '',
  }
})

const dockerOk = computed(() => list.value?.dockerOk ?? false)

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    list.value = await api.listImages()
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

onBeforeUnmount(() => {
  for (const tag of Object.keys(timers)) {
    window.clearInterval(timers[tag])
    delete timers[tag]
  }
})

function stopPoll(tag: string): void {
  if (timers[tag] !== undefined) {
    window.clearInterval(timers[tag])
    delete timers[tag]
  }
  const next = { ...pulling.value }
  delete next[tag]
  pulling.value = next
}

function poll(tag: string, jobId: string): void {
  timers[tag] = window.setInterval(async () => {
    try {
      const job = await api.getJob(jobId)
      if (pulling.value[tag]) {
        pulling.value = { ...pulling.value, [tag]: { jobId, progress: job.progress } }
      }
      if (job.status === 'running') return
      stopPoll(tag)
      if (job.status === 'succeeded') {
        ElMessage.success(`${tag} 已拉取到本机`)
      } else {
        ElMessage.error(job.errorHint || '拉取失败，去任务中心看详情')
      }
      await load()
    } catch (e) {
      stopPoll(tag)
      ElMessage.error(errText(e))
    }
  }, 2000)
}

async function pull(img: ImageInfo): Promise<void> {
  if (pulling.value[img.tag]) return
  try {
    const res = await api.pullImage(img.tag, img.family)
    if (res.cached) {
      ElMessage.success('本地已有这个镜像')
      await load()
      return
    }
    if (!res.jobId) {
      ElMessage.error('后端没有返回任务号')
      return
    }
    pulling.value = { ...pulling.value, [img.tag]: { jobId: res.jobId, progress: 5 } }
    poll(img.tag, res.jobId)
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

async function remove(img: ImageInfo): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除 ${img.name} 后，下次用这个版本部署时要重新拉取（可能几分钟）。服务与数据不受影响。`,
      '删除本地镜像',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    const res = await api.deleteImage(img.tag, img.family)
    ElMessage.success(res.message)
    await load()
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

function usedText(img: ImageInfo): string {
  const n = img.usedBy?.length ?? 0
  if (!n) return ''
  if (n === 1) return `1 个服务在用：${img.usedBy[0]}`
  return `${n} 个服务在用`
}
</script>

<template>
  <div class="page">
    <header class="head">
      <div>
        <h2>镜像</h2>
        <p class="muted">
          {{ engineLabel(engine) }} 的版本就是镜像 tag，一个 tag 一个版本。可以现在就拉好，部署时不用再等；
          删掉镜像只是省磁盘，服务与数据都不动。
        </p>
      </div>
      <div class="row">
        <span class="pill" :class="dockerOk ? 'pill-ok' : 'pill-bad'">
          Docker {{ dockerOk ? '可用' : '不可用' }}
        </span>
        <button type="button" class="btn" :disabled="loading" @click="load">
          {{ loading ? '读取中' : '刷新' }}
        </button>
      </div>
    </header>

    <EngineTabs v-model="engine" />

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load">重试</button>
    </div>

    <div v-if="!dockerOk" class="notice notice-bad">
        <span>读不到 Docker。请确认服务已加入 docker 用户组、且 /var/run/docker.sock 可访问；修好之前无法部署服务。</span>
      </div>

      <section class="stats">
        <div class="stat">
          <strong>{{ stats.total }}</strong>
          <span>可部署版本</span>
        </div>
        <div class="stat">
          <strong>{{ stats.local }}</strong>
          <span>已在本机</span>
        </div>
        <div class="stat">
          <strong>{{ stats.used }}</strong>
          <span>服务正在用</span>
        </div>
        <div class="stat">
          <strong class="mono">{{ stats.arch }}</strong>
          <span>当前架构</span>
        </div>
      </section>

      <p v-if="!loading && stats.total" class="catalog-note">
        已加载 {{ stats.total }} 个可选版本，本机已有 {{ stats.local }} 个；未拉取的版本仍会出现在下方，可点「立即拉取」。
      </p>

      <p v-if="!loading && !groups.length" class="muted">
        这个引擎暂时没有可部署的版本。换到 MySQL / PostgreSQL / MongoDB 页签看看，或点右上角「刷新」。
      </p>

      <section v-for="g in groups" :key="g.family" class="group">
        <header class="group-head">
          <h3>{{ g.title }}</h3>
          <span class="muted small">已就绪 {{ g.ready }} / {{ g.items.length }}</span>
        </header>

        <article v-for="img in g.items" :key="img.tag" class="ver" :class="{ dim: !img.supports }">
          <div class="ver-main">
            <div class="ver-title">
              <strong>{{ img.label }}</strong>
              <span v-if="img.default" class="pill pill-accent">默认</span>
              <span v-if="!img.supports" class="pill pill-bad">本机架构不支持</span>
            </div>
            <p class="ver-meta">
              <code>{{ img.name }}</code>
              <span class="sep">·</span>
              <span v-for="(a, i) in img.archs" :key="a" class="arch">
                {{ a }}<span v-if="i < img.archs.length - 1"> / </span>
              </span>
              <span v-if="img.note" class="sep">·</span>
              <span v-if="img.note">{{ img.note }}</span>
            </p>
            <p v-if="!img.supports" class="ver-warn">
              官方没有发布 {{ stats.arch }} 架构的镜像，这台机器上没法用它，换一个版本或换机器。
            </p>
          </div>

          <div class="ver-side">
            <div class="ver-state">
              <span v-if="pulling[img.tag]" class="pill pill-accent">
                拉取中 {{ pulling[img.tag].progress }}%
              </span>
              <span v-else-if="img.local" class="pill pill-ok">已在本机</span>
              <span v-else class="pill pill-warn">未拉取</span>
              <span v-if="img.usedBy?.length" class="used">{{ usedText(img) }}</span>
            </div>
            <div class="ver-ops">
              <template v-if="pulling[img.tag]">
                <span class="muted small">拉取中…</span>
              </template>
              <template v-else-if="img.local">
                <button
                  type="button"
                  class="btn btn-sm"
                  :disabled="!!img.usedBy?.length"
                  :title="img.usedBy?.length ? '有服务在用，先删服务或换版本' : ''"
                  @click="remove(img)"
                >
                  删除
                </button>
              </template>
              <template v-else>
                <button
                  type="button"
                  class="btn btn-sm btn-primary"
                  :disabled="!img.supports || !dockerOk"
                  @click="pull(img)"
                >
                  立即拉取
                </button>
              </template>
            </div>
          </div>
        </article>
      </section>

      <p class="muted small foot">
        当前架构 {{ stats.arch }}；<code>amd64</code> 指 Intel/AMD 的 NAS，<code>arm64</code> 指 ARM 的 NAS。
        离线机器可以先在别处 <code>docker save</code>，再 <code>docker load</code> 到这台机器。
    </p>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.head h2 {
  margin: 0 0 4px;
  font-size: 18px;
}

.head p {
  margin: 0;
  max-width: 720px;
  font-size: 13.5px;
  line-height: 1.6;
}

/* ---------- 统计条 ---------- */

.stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 12px 16px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.stat strong {
  font-size: 22px;
  font-weight: 600;
  line-height: 1.2;
}

.stat span {
  color: var(--fg-muted);
  font-size: 12.5px;
}

/* ---------- 版本行 ---------- */

.group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.group-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 0 2px;
}

.group-head h3 {
  margin: 0;
  font-size: 14px;
}

.ver {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 16px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.ver.dim {
  background: var(--bg);
}

.ver-main {
  min-width: 0;
}

.ver-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.ver-title strong {
  font-size: 14px;
}

.ver-meta {
  margin: 4px 0 0;
  color: var(--fg-muted);
  font-size: 12.5px;
  line-height: 1.6;
}

.ver-meta code {
  color: var(--fg);
}

.sep {
  margin: 0 6px;
}

.arch {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}

.ver-warn {
  margin: 6px 0 0;
  color: var(--el-color-danger);
  font-size: 12.5px;
}

.ver-side {
  display: flex;
  flex: none;
  align-items: center;
  gap: 14px;
}

.ver-state {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 4px;
}

.used {
  color: var(--accent);
  font-size: 12px;
}

.ver-ops {
  min-width: 84px;
  text-align: right;
}

.foot {
  margin: 0;
  line-height: 1.7;
}

.catalog-note {
  margin: -4px 2px 0;
  color: var(--fg-muted);
  font-size: 12.5px;
}

@media (max-width: 900px) {
  .stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .ver {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .ver-side {
    justify-content: space-between;
  }

  .ver-state {
    align-items: flex-start;
  }

  .ver-ops {
    text-align: left;
  }
}

@media (max-width: 720px) {
  .head {
    flex-direction: column;
    gap: 10px;
  }

  .head h2 {
    font-size: 17px;
  }
}
</style>
