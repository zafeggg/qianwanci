<script setup>
/**
 * 千万次 DApp - 连接钱包守卫
 * 未连接钱包时展示引导，连接后渲染插槽内容（懒加载 WalletModal 避免循环依赖）
 */
import { ref } from 'vue';
import { useWalletStore } from '../stores/wallet.js';
import WalletModal from './WalletModal.vue';

const walletStore = useWalletStore();
const showModal = ref(false);
</script>

<template>
  <!--
    ⚠ 必须是**单一根元素**：本组件被放在 App.vue 的
    <router-view v-slot><transition name="page-fade" mode="out-in"> 里，
    而 <transition> 要求被包裹组件只能有一个根节点。
    原实现是 `<div v-if>…</div><slot v-else/>`（多根片段），Vue 会报
    「Component inside <Transition> renders non-element root node that cannot be animated」，
    后果是**第一次路由切换之后视图不再显示（页面白屏，只剩底部 tab）** —— 2026-09-21 实测复现并修复。
  -->
  <div class="connect-guard">
    <div v-if="!walletStore.connected" class="empty">
      <div class="empty__icon">🔗</div>
      <p>请先连接钱包</p>
      <button class="btn btn--primary btn--block mt-md" @click="showModal = true">
        连接钱包
      </button>
      <WalletModal v-model:show="showModal" />
    </div>
    <slot v-else />
  </div>
</template>
