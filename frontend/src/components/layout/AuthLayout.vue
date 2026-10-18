<template>
  <div class="relative flex min-h-screen items-center justify-center overflow-hidden bg-[#f6f8fc] p-4 dark:bg-dark-950 sm:p-6">
    <!-- Background -->
    <div
      class="absolute inset-0 hidden bg-gradient-to-br dark:block dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
    ></div>

    <!-- Decorative Elements -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="absolute -left-24 -top-32 h-[520px] w-[520px] rounded-full bg-blue-400/15 blur-[120px]"></div>
      <div class="absolute -right-32 top-[20%] h-[600px] w-[600px] rounded-full bg-indigo-400/[0.12] blur-[140px]"></div>
      <div class="absolute -bottom-40 left-[30%] h-[560px] w-[560px] rounded-full bg-sky-300/15 blur-[130px]"></div>
      <div
        class="absolute inset-0 bg-[linear-gradient(rgba(59,130,246,0.035)_1px,transparent_1px),linear-gradient(90deg,rgba(59,130,246,0.035)_1px,transparent_1px)] bg-[size:64px_64px] [mask-image:radial-gradient(ellipse_at_center,black,transparent_75%)]"
      ></div>
    </div>

    <div class="relative z-10 grid w-full max-w-6xl items-center gap-12 lg:grid-cols-[1fr_minmax(0,28rem)] lg:gap-16">
      <!-- 宽屏左侧品牌区，复用首页的卖点文案 -->
      <section v-if="settingsLoaded" class="hidden lg:block">
        <div class="flex items-center gap-3">
          <div class="h-12 w-12 overflow-hidden rounded-2xl shadow-glass ring-1 ring-white/70">
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <span class="text-xl font-bold tracking-tight text-slate-900 dark:text-white">{{ siteName }}</span>
        </div>
        <h1 class="mt-10 bg-gradient-to-r from-slate-900 via-primary-700 to-indigo-600 bg-clip-text pb-1 text-5xl font-bold tracking-tight text-transparent dark:from-white dark:via-white dark:to-white">
          {{ siteName }}
        </h1>
        <p class="mt-4 max-w-xl text-lg leading-relaxed text-slate-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <div class="mt-10 grid max-w-xl gap-4">
          <div v-for="feature in features" :key="feature.title" class="card flex items-start gap-4 p-5">
            <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl ring-1 ring-inset" :class="feature.tone">
              <Icon :name="feature.icon" size="md" />
            </span>
            <div class="min-w-0">
              <p class="font-semibold tracking-tight text-slate-900 dark:text-white">{{ feature.title }}</p>
              <p class="mt-1 text-sm leading-relaxed text-slate-500 dark:text-dark-400">{{ feature.desc }}</p>
            </div>
          </div>
        </div>
      </section>

      <!-- Content Container -->
      <div class="mx-auto w-full max-w-md lg:max-w-none">
        <!-- Logo/Brand（窄屏） -->
        <div class="mb-8 text-center lg:hidden">
          <template v-if="settingsLoaded">
            <div
              class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl shadow-glass ring-1 ring-white/70"
            >
              <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
            </div>
            <h1 class="text-gradient mb-2 text-3xl font-bold tracking-tight">
              {{ siteName }}
            </h1>
            <p class="text-sm text-slate-500 dark:text-dark-400">
              {{ siteSubtitle }}
            </p>
          </template>
        </div>

        <!-- Card Container -->
        <div class="card-glass rounded-2xl p-8 shadow-glass-lg">
          <slot />
        </div>

        <!-- Footer Links -->
        <div class="mt-6 text-center text-sm">
          <slot name="footer" />
        </div>

        <!-- Copyright -->
        <div class="mt-8 text-center text-xs text-slate-400 dark:text-dark-500">
          &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

const { t } = useI18n()
const features = computed(() => [
  { icon: 'server' as const, tone: 'bg-blue-50 text-blue-600 ring-blue-500/15', title: t('home.features.unifiedGateway'), desc: t('home.features.unifiedGatewayDesc') },
  { icon: 'shield' as const, tone: 'bg-indigo-50 text-indigo-600 ring-indigo-500/15', title: t('home.features.multiAccount'), desc: t('home.features.multiAccountDesc') },
  { icon: 'chart' as const, tone: 'bg-violet-50 text-violet-600 ring-violet-500/15', title: t('home.features.balanceQuota'), desc: t('home.features.balanceQuotaDesc') }
])

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.text-gradient {
  @apply bg-gradient-to-r from-primary-600 to-indigo-500 bg-clip-text text-transparent;
}
</style>
