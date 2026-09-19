<template>
  <div class="landing">
    <header>
      <a class="brand" href="#" :aria-label="t('landingHome.homeLabel', { siteName })">
        <img v-if="siteLogo" :src="siteLogo" class="site-logo" alt="" />
        <span v-else class="brandmark" aria-hidden="true"><i></i><i></i></span>
        {{ siteName }}
      </a>
      <nav :aria-label="t('landingHome.navigationLabel')">
        <router-link v-if="showModelPlazaEntry" to="/model-plaza">{{ t('landingHome.modelPlaza') }}</router-link
        ><a href="#workspace">{{ t('landingHome.workspace') }}</a
        ><a
          :href="docUrl || '#connect'"
          :target="docUrl ? '_blank' : undefined"
          rel="noopener noreferrer"
          >{{ t('landingHome.docs') }}</a
        >
      </nav>
      <div class="nav-end">
        <LocaleSwitcher />
        <router-link v-if="!isAuthenticated" class="landing-login" to="/login">{{ t('landingHome.login') }}</router-link
        ><router-link class="btn small" :to="dashboardPath"
          >{{ t('landingHome.console') }}</router-link
        >
      </div>
    </header>
    <main class="shell">
      <section class="hero">
        <div class="hero-copy">
          <div class="eyebrow">ONE KEY. MORE POSSIBILITIES.</div>
          <h1>{{ t('landingHome.heroTitleLine1') }}<br /><span>{{ t('landingHome.heroTitleLine2') }}</span></h1>
          <p class="intro">
            {{ t('landingHome.heroIntroLine1') }}<br />{{ t('landingHome.heroIntroLine2') }}<br />{{ t('landingHome.heroIntroLine3') }}
          </p>
          <div class="actions">
            <router-link class="btn" to="/keys"
              >{{ t('landingHome.start') }} <span>↗</span></router-link
            ><a class="btn light" href="#connect"
              >{{ t('landingHome.viewExample') }} <span>↓</span></a
            >
          </div>
          <p class="hero-note">{{ t('landingHome.heroNote') }}</p>
        </div>
        <div
          class="studio"
          role="img"
          :aria-label="t('landingHome.conceptLabel')"
        >
          <div class="studio-label">THE POSSIBILITY ENGINE</div>
          <div class="studio-label right">FIG. 001</div>
          <div class="orbit"><i class="orbit-dot"></i></div>
          <div class="core"><strong>AI</strong><small>AI GATEWAY</small></div>
          <div class="node n1">
            <b><span class="symbol">⌘</span>Code</b
            ><small>BUILD SOMETHING</small>
          </div>
          <div class="node n2">
            <b><span class="symbol">✳</span>Think</b
            ><small>EXPLORE IDEAS</small>
          </div>
          <div class="node n3">
            <b><span class="symbol">▧</span>Imagine</b
            ><small>MAKE IT VISIBLE</small>
          </div>
          <div class="node n4">
            <b><span class="symbol">↗</span>Create</b><small>GO BEYOND</small>
          </div>
          <div class="studio-bottom">
            <span>ONE ENDPOINT → YOUR NEXT IDEA</span
            ><span>CONCEPT / NOT LIVE DATA</span>
          </div>
        </div>
      </section>
      <div class="partners" id="models">
        <small>{{ t('landingHome.modelsHint') }}<br />{{ t('landingHome.modelsDisclaimer') }}</small
        ><span>OpenAI</span><span>Claude</span><span>Gemini</span
        ><span class="mono">DeepSeek</span><span class="mono">Qwen</span>
      </div>
      <section class="section" id="workspace">
        <div class="section-heading">
          <div>
            <div class="eyebrow">BUILT FOR YOUR FLOW</div>
            <h2>{{ t('landingHome.possibilitiesTitle') }}</h2>
          </div>
          <p>
            {{ t('landingHome.possibilitiesDescription') }}<br />{{ t('landingHome.possibilitiesPrompt') }}
          </p>
        </div>
        <div class="features">
          <article class="feature">
            <span class="num">01 / DEVELOP</span>
            <h3>{{ t('landingHome.developTitle') }}</h3>
            <p>{{ t('landingHome.developDescription') }}</p>
            <a href="#connect">{{ t('landingHome.viewIntegration') }}</a>
          </article>
          <article class="feature">
            <span class="num">02 / EXPLORE</span>
            <h3>{{ t('landingHome.exploreTitle') }}</h3>
            <p>{{ t('landingHome.exploreDescription') }}</p>
            <router-link :to="dashboardPath">{{ t('landingHome.exploreConsole') }}</router-link>
          </article>
          <article class="feature canvas">
            <span class="num">03 / CREATE</span>
            <div class="mini-art" aria-hidden="true"></div>
            <h3>{{ t('landingHome.createTitle') }}</h3>
            <p>{{ t('landingHome.createDescription') }}</p>
            <router-link to="/workspace/canvas">{{ t('landingHome.openCanvas') }}</router-link>
          </article>
        </div>
      </section>
      <section class="section integration" id="connect">
        <div>
          <div class="eyebrow">LESS SETUP. MORE BUILDING.</div>
          <h2>{{ t('landingHome.requestTitle') }}</h2>
          <p>
            {{ t('landingHome.requestDescription') }}<br />{{ t('landingHome.requestPrompt') }}
          </p>
          <ol class="steps">
            <li><b>01</b>{{ t('landingHome.stepCreateKey') }}</li>
            <li><b>02</b>{{ t('landingHome.stepSelectModel') }}</li>
            <li><b>03</b>{{ t('landingHome.stepSendRequest') }}</li>
          </ol>
        </div>
        <div class="codebox">
          <div class="codehead">
            <div class="tabs" role="tablist" :aria-label="t('landingHome.exampleLanguage')">
              <button
                v-for="lang in languages"
                :key="lang"
                role="tab"
                :id="`tab-${lang}`"
                :aria-selected="language === lang"
                aria-controls="home-code"
                :tabindex="language === lang ? 0 : -1"
                @click="language = lang"
                @keydown="changeTab($event, lang)"
              >
                {{ lang === 'python' ? 'Python' : 'cURL' }}
              </button>
            </div>
            <button class="copy" @click="copyCode">{{ t('landingHome.copyCode') }}</button>
          </div>
          <pre
            ref="codeElement"
            class="codebody"
            id="home-code"
            role="tabpanel"
            :aria-labelledby="`tab-${language}`"
            tabindex="0"
            >{{ code }}</pre
          >
          <div class="codefoot">
            {{ t('landingHome.codeDisclaimer') }}
          </div>
        </div>
      </section>
      <section class="faq">
        <div>
          <div class="eyebrow">GOOD TO KNOW</div>
          <h2>{{ t('landingHome.faqTitle') }}</h2>
        </div>
        <div>
          <details>
            <summary>{{ t('landingHome.faqToolsQuestion') }}</summary>
            <p>{{ t('landingHome.faqToolsAnswer') }}</p>
          </details>
          <details>
            <summary>{{ t('landingHome.faqPricingQuestion') }}</summary>
            <p>{{ t('landingHome.faqPricingAnswer') }}</p>
          </details>
          <details>
            <summary>{{ t('landingHome.faqCanvasQuestion') }}</summary>
            <p>{{ t('landingHome.faqCanvasAnswer') }}</p>
          </details>
        </div>
      </section>
      <section class="final-cta">
        <div>
          <h2>{{ t('landingHome.finalTitle') }}</h2>
          <p>{{ t('landingHome.finalDescription') }}</p>
        </div>
        <router-link class="btn" to="/keys">{{ t('landingHome.createKey') }}</router-link>
      </section>
    </main>
    <footer class="shell">
      <span>{{ siteName }} / {{ siteHost }}</span
      ><span>© {{ new Date().getFullYear() }} {{ siteName }}</span
      ><span>DESIGNED FOR WHAT'S NEXT.</span>
    </footer>
    <div class="toast" role="status">{{ toast }}</div>
  </div>
</template>
<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
const app = useAppStore()
const auth = useAuthStore()
const { t } = useI18n()
const siteName = computed(
  () => app.cachedPublicSettings?.site_name || app.siteName || 'Sub2API'
)
const siteLogo = computed(() =>
  sanitizeUrl(app.cachedPublicSettings?.site_logo || app.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true
  })
)
const siteHost = window.location.host
const isAuthenticated = computed(() => auth.isAuthenticated)
const dashboardPath = computed(() =>
  auth.isAdmin ? '/admin/dashboard' : '/dashboard'
)
const docUrl = computed(() =>
  sanitizeUrl(app.cachedPublicSettings?.doc_url || app.docUrl || '')
)
const showModelPlazaEntry = computed(
  () =>
    isFeatureFlagEnabled(FeatureFlags.modelPlaza) &&
    (auth.isAuthenticated ||
      app.cachedPublicSettings?.model_plaza_require_auth !== true)
)
const baseUrl = computed(() => {
  const configured = sanitizeUrl(app.cachedPublicSettings?.api_base_url || '')
  const base = (configured || window.location.origin).replace(/\/+$/, '')
  return base.endsWith('/v1') ? base : `${base}/v1`
})
const languages = ['python', 'curl'] as const
type Language = (typeof languages)[number]
const language = ref<Language>('python')
const codeElement = ref<HTMLElement | null>(null)
const toast = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
const code = computed(() =>
  language.value === 'python'
    ? `from openai import OpenAI

client = OpenAI(
    base_url=${JSON.stringify(baseUrl.value)},
    api_key="YOUR_API_KEY"
)

response = client.responses.create(
    model="YOUR_MODEL",
    input="Hello, let's build something."
)
print(response.output_text)`
    : `curl '${baseUrl.value.replace(/'/g, "'\\''")}/responses' \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "YOUR_MODEL",
    "input": "Hello, let us build something."
  }'`
)
function changeTab(event: KeyboardEvent, lang: Language) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  language.value =
    event.key === 'Home'
      ? 'python'
      : event.key === 'End'
        ? 'curl'
        : lang === 'python'
          ? 'curl'
          : 'python'
  const group = (event.currentTarget as HTMLElement).parentElement
  group?.querySelector<HTMLButtonElement>(`#tab-${language.value}`)?.focus()
}
async function copyCode() {
  try {
    await navigator.clipboard.writeText(code.value)
    toast.value = t('landingHome.copied')
  } catch {
    if (codeElement.value) {
      const range = document.createRange()
      range.selectNodeContents(codeElement.value)
      window.getSelection()?.removeAllRanges()
      window.getSelection()?.addRange(range)
    }
    toast.value = t('landingHome.selected')
  }
  clearTimeout(timer)
  timer = setTimeout(() => {
    toast.value = ''
  }, 3500)
}
onUnmounted(() => clearTimeout(timer))
</script>
<style scoped>
.landing {
  --paper: #f5f4ed;
  --ink: #173f35;
  --muted: #737b70;
  --line: #d9dfd3;
  --orange: #e77846;
  --green: #d9e6b9;
}
* {
  box-sizing: border-box;
}
.landing {
  margin: 0;
  background: var(--paper);
  color: var(--ink);
  font-family: 'PingFang SC', 'Microsoft YaHei', sans-serif;
}
a {
  color: inherit;
  text-decoration: none;
}
button {
  font: inherit;
  color: inherit;
  cursor: pointer;
}
button,
a {
  -webkit-tap-highlight-color: transparent;
}
a:focus-visible,
button:focus-visible,
summary:focus-visible {
  outline: 3px solid var(--orange);
  outline-offset: 5px;
}
::selection {
  background: #d9e6b9;
}
header {
  height: 88px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--line);
  max-width: 1440px;
  margin: auto;
  padding: 0 5%;
}
.brand {
  font-size: 25px;
  font-weight: 800;
  letter-spacing: -1.5px;
  display: flex;
  align-items: center;
  gap: 12px;
}
.site-logo {
  width: 32px;
  height: 32px;
  object-fit: contain;
  flex-shrink: 0;
}
.brandmark {
  display: grid;
  grid-template-columns: repeat(2, 9px);
  gap: 4px;
  transform: rotate(-15deg);
}
.brandmark i {
  width: 9px;
  height: 18px;
  background: var(--ink);
  border-radius: 5px;
}
.brandmark i:last-child {
  background: var(--orange);
}
nav {
  display: flex;
  gap: 32px;
  font-size: 13px;
}
.nav-end {
  display: flex;
  align-items: center;
  gap: 25px;
  font-size: 13px;
}
.btn {
  border: 1px solid var(--ink);
  border-radius: 8px;
  background: var(--ink);
  color: var(--paper);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 28px;
  padding: 16px 23px;
  font-weight: 600;
  font-size: 14px;
  transition:
    transform 0.2s,
    background 0.2s;
}
.btn:hover {
  transform: translateY(-3px);
  background: #295747;
}
.btn.light {
  background: transparent;
  color: var(--ink);
  border-color: #b7c4b6;
}
.btn.small {
  padding: 11px 16px;
}
.shell {
  max-width: 1440px;
  margin: auto;
  padding: 0 5%;
}
.hero {
  display: grid;
  grid-template-columns: 1.05fr 1fr;
  gap: 4%;
  align-items: center;
  min-height: 660px;
  padding: 66px 0 55px;
}
.eyebrow {
  font:
    11px 'Courier New',
    monospace;
  letter-spacing: 2px;
  display: flex;
  align-items: center;
  gap: 12px;
}
.eyebrow:before {
  content: '';
  width: 7px;
  height: 7px;
  background: var(--orange);
  border-radius: 50%;
}
h1 {
  font-size: clamp(48px, 5.15vw, 76px);
  letter-spacing: -4px;
  line-height: 1.2;
  margin: 30px 0 25px;
  font-weight: 650;
}
h1 span {
  font-family: Georgia, 'Songti SC', serif;
  font-weight: 400;
  font-style: italic;
  color: #78906b;
  position: relative;
}
h1 span:after {
  content: '';
  position: absolute;
  bottom: -8px;
  left: 2%;
  width: 96%;
  height: 7px;
  border-top: 2px solid var(--orange);
  border-radius: 50%;
  transform: rotate(-2deg);
}
.intro {
  font-size: 15px;
  line-height: 1.95;
  color: #69746b;
  max-width: 420px;
}
.actions {
  display: flex;
  gap: 12px;
  margin-top: 32px;
}
.hero-note {
  font-size: 11px;
  letter-spacing: 1px;
  color: var(--muted);
  margin-top: 22px;
}
.studio {
  height: 478px;
  position: relative;
  background: #e9eddf;
  border: 1px solid #dbe0d2;
  border-radius: 18px;
  overflow: hidden;
  display: grid;
  place-items: center;
  background-image: radial-gradient(#aebda766 0.8px, transparent 0.8px);
  background-size: 20px 20px;
}
.studio-label {
  position: absolute;
  top: 22px;
  left: 25px;
  font:
    10px 'Courier New',
    monospace;
  letter-spacing: 2px;
  color: #76836c;
}
.studio-label.right {
  left: auto;
  right: 23px;
}
.orbit {
  position: absolute;
  width: 350px;
  height: 350px;
  border: 1px solid #b4c3a6;
  border-radius: 50%;
  transform: rotate(-30deg);
}
.orbit:before,
.orbit:after {
  content: '';
  position: absolute;
  inset: 40px -42px;
  border: 1px solid #b4c3a6;
  border-radius: 50%;
}
.orbit:after {
  inset: -40px 45px;
}
.core {
  width: 148px;
  height: 148px;
  background: var(--ink);
  color: #eef3d8;
  border-radius: 30px;
  display: grid;
  place-content: center;
  text-align: center;
  box-shadow: 0 20px 40px #173f3525;
  z-index: 1;
  transform: rotate(-7deg);
}
.core strong {
  font:
    italic 58px Georgia,
    serif;
  letter-spacing: -5px;
}
.core small {
  font: 9px 'Courier New';
  letter-spacing: 3px;
}
.node {
  position: absolute;
  z-index: 2;
  background: #fafbf4;
  border: 1px solid #ccd5be;
  padding: 13px 17px;
  border-radius: 9px;
  box-shadow: 0 6px 18px #344c3010;
  min-width: 116px;
}
.node b {
  font-size: 14px;
}
.node small {
  display: block;
  font: 9px 'Courier New';
  letter-spacing: 1px;
  margin-top: 5px;
  color: #7c8674;
}
.n1 {
  top: 82px;
  left: 35px;
}
.n2 {
  right: 27px;
  top: 112px;
}
.n3 {
  left: 27px;
  bottom: 116px;
}
.n4 {
  right: 35px;
  bottom: 85px;
}
.node .symbol {
  margin-right: 8px;
  color: var(--orange);
}
.studio-bottom {
  position: absolute;
  bottom: 23px;
  left: 25px;
  right: 25px;
  display: flex;
  justify-content: space-between;
  font: 9px 'Courier New';
  letter-spacing: 1px;
  color: #76836c;
}
.orbit-dot {
  position: absolute;
  width: 11px;
  height: 11px;
  border-radius: 50%;
  background: var(--orange);
  top: 47px;
  left: 48px;
}
.partners {
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  padding: 26px 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}
.partners small {
  font-size: 11px;
  color: var(--muted);
  line-height: 1.7;
}
.partners span {
  font:
    24px Georgia,
    serif;
  letter-spacing: -0.8px;
}
.partners .mono {
  font-family: 'Courier New';
  font-size: 21px;
  font-weight: bold;
}
.section {
  padding: 85px 0;
}
.section-heading {
  display: flex;
  justify-content: space-between;
  align-items: end;
  margin-bottom: 32px;
  gap: 25px;
}
h2 {
  font-size: 36px;
  letter-spacing: -1.5px;
  font-weight: 600;
  margin: 16px 0 0;
}
.section-heading p {
  font-size: 13px;
  color: var(--muted);
  line-height: 1.8;
  margin: 0;
}
.features {
  display: grid;
  grid-template-columns: 1fr 1fr 1.1fr;
  gap: 18px;
}
.feature {
  padding: 30px;
  border: 1px solid var(--line);
  border-radius: 12px;
  min-height: 255px;
  display: flex;
  flex-direction: column;
  align-items: start;
}
.feature .num {
  font: 11px 'Courier New';
  color: var(--muted);
}
.feature h3 {
  font-size: 21px;
  margin: 24px 0 10px;
}
.feature p {
  font-size: 13px;
  line-height: 1.9;
  color: var(--muted);
  margin: 0;
  max-width: 260px;
}
.feature > a {
  font-size: 12px;
  margin-top: auto;
  padding-top: 25px;
}
.feature.canvas {
  background: #e7ebda;
  position: relative;
  overflow: hidden;
}
.canvas .mini-art {
  position: absolute;
  right: 18px;
  top: 26px;
  width: 90px;
  height: 63px;
  border-radius: 5px;
  background: linear-gradient(150deg, #e6dabc 40%, #94a383 41%, #314f41);
  border: 5px solid #fff;
  transform: rotate(10deg);
  box-shadow: 0 6px 10px #183d3520;
}
.canvas .mini-art:before {
  content: '';
  width: 21px;
  height: 21px;
  border-radius: 50%;
  position: absolute;
  background: #e98c57;
  top: 5px;
  right: 15px;
}
.integration {
  padding-top: 0;
  display: grid;
  grid-template-columns: 0.85fr 1.15fr;
  gap: 70px;
  align-items: center;
}
.integration p {
  font-size: 14px;
  line-height: 1.9;
  color: var(--muted);
}
.steps {
  padding: 0;
  list-style: none;
  margin-top: 28px;
  display: grid;
  gap: 17px;
  font-size: 13px;
}
.steps li {
  display: flex;
  align-items: center;
  gap: 15px;
}
.steps b {
  font: 10px 'Courier New';
  border: 1px solid var(--line);
  border-radius: 50%;
  height: 27px;
  width: 27px;
  display: grid;
  place-items: center;
}
.codebox {
  background: #173a31;
  color: #e2ead7;
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 12px 35px #193e3510;
}
.codehead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 20px;
  border-bottom: 1px solid #ffffff20;
}
.tabs {
  display: flex;
  gap: 4px;
}
.tabs button {
  background: transparent;
  border: 0;
  color: #98ada0;
  padding: 7px 10px;
  font: 12px 'Courier New';
  border-radius: 5px;
}
.tabs button[aria-selected='true'] {
  background: #ffffff15;
  color: #e7efdc;
}
.copy {
  background: transparent;
  border: 1px solid #ffffff30;
  border-radius: 5px;
  padding: 6px 10px;
  color: #d6e2d0;
  font-size: 11px;
}
.codebody {
  padding: 26px 24px;
  overflow: auto;
  min-height: 235px;
  margin: 0;
  font:
    12px/1.95 'Courier New',
    monospace;
  white-space: pre;
}
.codefoot {
  border-top: 1px solid #ffffff15;
  font-size: 10px;
  color: #a7bba9;
  padding: 13px 24px;
}
.faq {
  border-top: 1px solid var(--line);
  padding: 58px 0;
  display: grid;
  grid-template-columns: 1fr 1.8fr;
  gap: 60px;
}
details {
  border-bottom: 1px solid var(--line);
  padding: 19px 0;
  font-size: 14px;
}
summary {
  cursor: pointer;
  list-style: none;
  display: flex;
  justify-content: space-between;
  gap: 15px;
}
summary::-webkit-details-marker {
  display: none;
}
summary:after {
  content: '+';
  font: 19px Georgia;
}
details[open] summary:after {
  content: '−';
}
details p {
  font-size: 13px;
  line-height: 1.9;
  color: var(--muted);
}
.final-cta {
  background: #dce7c7;
  padding: 45px;
  border-radius: 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 30px;
  margin: 12px 0 60px;
}
.final-cta h2 {
  margin: 0;
  font-size: 32px;
}
.final-cta p {
  font-size: 13px;
  color: #6c7c60;
  margin-bottom: 0;
}
footer {
  border-top: 1px solid var(--line);
  padding: 25px 0 32px;
  display: flex;
  justify-content: space-between;
  font-size: 10px;
  color: var(--muted);
  gap: 20px;
}
.preview {
  font: 10px 'Courier New';
  letter-spacing: 1px;
  padding: 6px 9px;
  background: #e8ecdf;
  border-radius: 4px;
}
.toast {
  position: fixed;
  bottom: 24px;
  left: 50%;
  transform: translateX(-50%);
  padding: 12px 20px;
  background: #173f35;
  color: white;
  border-radius: 8px;
  font-size: 12px;
  z-index: 5;
  box-shadow: 0 5px 25px #0002;
  max-width: 90vw;
  text-align: center;
}
.toast:empty {
  display: none;
}
@media (min-width: 900px) {
  .studio {
    animation: arrive 0.9s ease-out both;
  }
  .hero-copy {
    animation: arrive 0.7s ease-out both;
  }
  @keyframes arrive {
    from {
      opacity: 0;
      transform: translateY(16px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
}
@media (max-width: 900px) {
  nav {
    gap: 18px;
  }
  .hero {
    gap: 24px;
  }
  h1 {
    font-size: 50px;
  }
  .studio {
    height: 420px;
  }
  .orbit {
    width: 270px;
    height: 270px;
  }
  .node {
    padding: 10px;
    min-width: 90px;
  }
  .n1 {
    left: 12px;
  }
  .n2 {
    right: 12px;
  }
  .n3 {
    left: 12px;
  }
  .n4 {
    right: 12px;
  }
  .integration {
    gap: 30px;
  }
  .feature {
    padding: 22px;
  }
}
@media (max-width: 680px) {
  header {
    height: 74px;
    padding: 0 6%;
  }
  .brand {
    font-size: 22px;
  }
  nav {
    display: none;
  }
  .nav-end {
    gap: 12px;
  }
  .landing-login {
    display: none;
  }
  .shell {
    padding: 0 6%;
  }
  .hero {
    grid-template-columns: 1fr;
    padding: 44px 0 30px;
    gap: 32px;
  }
  h1 {
    font-size: 37px;
    letter-spacing: -2px;
    margin-top: 24px;
  }
  .intro {
    font-size: 14px;
  }
  .actions .btn {
    padding: 14px 18px;
    gap: 18px;
  }
  .studio {
    height: 365px;
  }
  .core {
    width: 125px;
    height: 125px;
  }
  .core strong {
    font-size: 50px;
  }
  .n1 {
    top: 62px;
    left: 17px;
  }
  .n2 {
    top: 86px;
    right: 12px;
  }
  .n3 {
    bottom: 95px;
    left: 12px;
  }
  .n4 {
    bottom: 58px;
    right: 15px;
  }
  .studio-label {
    font-size: 8px;
    left: 16px;
  }
  .studio-label.right {
    right: 16px;
  }
  .studio-bottom {
    font-size: 7px;
    left: 16px;
    right: 16px;
  }
  .partners {
    flex-wrap: wrap;
    justify-content: flex-start;
    gap: 20px 27px;
  }
  .partners small {
    width: 100%;
  }
  .partners span {
    font-size: 22px;
  }
  .section {
    padding: 50px 0;
  }
  .section-heading {
    display: block;
  }
  h2 {
    font-size: 29px;
  }
  .section-heading p {
    margin-top: 18px;
  }
  .features {
    grid-template-columns: 1fr;
  }
  .feature {
    min-height: 225px;
  }
  .integration {
    padding-top: 0;
    grid-template-columns: 1fr;
    gap: 25px;
  }
  .codebody {
    font-size: 11px;
    padding: 20px 16px;
  }
  .faq {
    grid-template-columns: 1fr;
    gap: 10px;
    padding: 40px 0;
  }
  .final-cta {
    padding: 28px;
    align-items: start;
    flex-direction: column;
    margin-bottom: 35px;
  }
  .final-cta h2 {
    font-size: 27px;
  }
  footer {
    flex-direction: column;
    gap: 12px;
  }
  .preview {
    display: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  *,
  *:before,
  *:after {
    animation: none !important;
    transition: none !important;
  }
}

.landing {
  color-scheme: light;
  min-height: 100vh;
}
.landing h1,
.landing h2,
.landing h3,
.landing p {
  color: inherit;
}
.landing .intro,
.landing .feature p,
.landing .integration p,
.landing details p {
  color: var(--muted);
}
.landing button {
  line-height: normal;
}
.landing .brand {
  min-width: 0;
  overflow-wrap: anywhere;
}
.landing .brandmark {
  flex-shrink: 0;
}
.landing section {
  scroll-margin-top: 24px;
}
.landing .studio .core {
  color: #eef3d8;
}
.landing .codebox {
  color: #e2ead7;
}
</style>
