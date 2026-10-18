<template>
  <div class="min-h-screen bg-[#f6f8fc] dark:bg-dark-950">
    <!-- 环境光晕：玻璃面板的半透明需要背后有柔和的色彩层次 -->
    <div class="pointer-events-none fixed inset-0 overflow-hidden dark:hidden">
      <div class="absolute -left-20 -top-24 h-[500px] w-[500px] rounded-full bg-blue-400/15 blur-[120px]"></div>
      <div class="absolute -right-24 top-[30%] h-[600px] w-[600px] rounded-full bg-indigo-400/[0.12] blur-[140px]"></div>
      <div class="absolute -bottom-32 left-[20%] h-[550px] w-[550px] rounded-full bg-sky-300/15 blur-[130px]"></div>
    </div>
    <div class="pointer-events-none fixed inset-0 hidden bg-mesh-gradient dark:block"></div>

    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative min-h-screen transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64']"
    >
      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main class="p-4 md:p-6 lg:p-8">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>
