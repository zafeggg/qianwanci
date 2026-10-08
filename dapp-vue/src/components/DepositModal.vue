<script setup>
/**
 * 千万次 DApp - 充值弹窗
 *
 * 链路：向**后端下发的收款地址**转 ERC20（TM），链上确认后由 cmd/evmwatch 扫链入账到本站余额。
 * ⚠ 因此这里只负责"把钱转出去 + 告诉用户要等确认"，不直接改本站账本。
 *
 * 设计取舍：
 *  - 收款地址/合约/精度/确认数全部来自 `GET /api/deposit/info`，前端不硬编码（写错就转丢钱）；
 *    该接口刻意不走 mock 降级，拿不到就显示"暂不可用"。
 *  - 链上余额用只读 provider 查（不触发钱包授权）；转账才用钱包签名。
 *  - 转账成功后提示"等待 N 个确认后到账"，并给出刷新入口 —— 不假装立刻到账。
 */
import { ref, computed, watch } from 'vue';
import { showToast } from 'vant';
import { useWalletStore } from '../stores/wallet.js';
import { fetchDepositInfo } from '../api/index.js';
import { readTokenBalance, transferToken } from '../services/token.js';
import { CHAIN_CONFIG } from '../constants/config.js';

const props = defineProps({
  show: { type: Boolean, default: false },
});
const emit = defineEmits(['update:show', 'deposited']);

const walletStore = useWalletStore();

const info = ref(null);        // /api/deposit/info 的返回
const loadError = ref('');     // 取不到充值信息时的原因
const onChain = ref(null);     // 链上余额（拿不到为 null → 显示 "--"）
const amount = ref('');
const busy = ref(false);
const lastHash = ref('');

const ready = computed(() => !!info.value?.enabled);
const chainName = computed(() => CHAIN_CONFIG.chainName);

// 打开即拉取。
// ⚠ 用 watch(props.show) 而不是只依赖 popup 的 @opened：弹窗组件事件的触发时机受动画与
//   渲染环境影响（无头浏览器里尤为明显），watch 更可靠，也不会重复请求（已打开时不重入）。
watch(
  () => props.show,
  (v) => { if (v) refresh(); },
  { immediate: true },
);

/** 拉取充值信息 + 链上余额 */
async function refresh() {
  loadError.value = '';
  try {
    info.value = await fetchDepositInfo();
  } catch (err) {
    info.value = null;
    loadError.value = err?.message || '充值通道暂不可用';
    return;
  }
  await refreshOnChain();
}

async function refreshOnChain() {
  const i = info.value;
  if (!i?.contract || !i?.rpcUrl || !walletStore.address) {
    onChain.value = null;
    return;
  }
  onChain.value = await readTokenBalance({
    rpcUrl: i.rpcUrl,
    contract: i.contract,
    owner: walletStore.address,
    decimals: i.decimals,
  });
}

/** 一键充值：钱包签名调用 ERC20.transfer(收款地址, 金额) */
async function doDeposit() {
  if (busy.value) return;
  if (!walletStore.connected || !walletStore.signer) {
    showToast('请先连接钱包');
    return;
  }
  busy.value = true;
  lastHash.value = '';
  try {
    const r = await transferToken({
      signer: walletStore.signer,
      contract: info.value.contract,
      to: info.value.address,
      amount: amount.value,
      decimals: info.value.decimals,
    });
    lastHash.value = r.hash;
    showToast('充值交易已上链，等待确认后到账');
    emit('deposited', r);
    await refreshOnChain();
  } catch (err) {
    //钱包拒绝 / 余额不足 / revert 都走这里，原文对用户更有用（例如 "insufficient funds"）
    showToast(err?.data?.message || err?.message || '充值失败');
  } finally {
    busy.value = false;
  }
}

async function copyAddress() {
  const addr = info.value?.address || '';
  if (!addr) return;
  try {
    await navigator.clipboard.writeText(addr);
    showToast('收款地址已复制');
  } catch {
    showToast('复制失败，请手动长按选择');
  }
}

function close() {
  emit('update:show', false);
}
</script>

<template>
  <van-popup
    :show="props.show"
    position="bottom"
    round
    :style="{ maxHeight: '78vh', paddingBottom: 'env(safe-area-inset-bottom)' }"
    @update:show="close"
  >
    <div class="deposit">
      <div class="deposit__header">
        <div class="deposit__title">充值</div>
        <button class="deposit__close" @click="close">✕</button>
      </div>

      <div v-if="loadError" class="empty">
        <div class="empty__icon">⚠️</div>
        <p>充值通道暂不可用</p>
        <p class="text-sm text-mute mt-sm">{{ loadError }}</p>
      </div>

      <div v-else-if="!ready" class="empty">
        <div class="empty__icon">⏳</div>
        <p>正在获取充值信息...</p>
      </div>

      <template v-else>
        <div class="card">
          <div class="text-sub text-sm">收款地址（{{ info.symbol }} · {{ chainName }}）</div>
          <div class="deposit__addr num">{{ info.address }}</div>
          <button class="btn btn--block mt-sm" @click="copyAddress">复制地址</button>
          <p class="text-sm text-mute mt-sm">
            只能转 {{ info.symbol }}（合约 {{ info.contract.slice(0, 10) }}…），
            转错币种或转错地址无法找回。
          </p>
          <p class="text-sm text-mute">
            转账后需等 <b>{{ info.confirmations }}</b> 个区块确认，系统才会入账到上面的余额。
          </p>
        </div>

        <div class="card">
          <div class="flex-between">
            <div class="text-sub text-sm">链上余额</div>
            <button class="deposit__refresh" @click="refreshOnChain">刷新</button>
          </div>
          <div class="deposit__balance num">
            {{ onChain === null ? '--' : onChain }} <span class="text-sm">{{ info.symbol }}</span>
          </div>
          <p class="text-sm text-mute mt-sm">
            链上余额与你在本站的余额是两回事：充值只增加链上余额，等确认入账后才进本站账本（资产页可查）。
          </p>
        </div>

        <div class="card">
          <div class="card__title">一键充值</div>
          <input
            v-model="amount"
            class="deposit__input num"
            type="number"
            inputmode="decimal"
            placeholder="输入要充值的数量"
          />
          <button class="btn btn--primary btn--block mt-md" :disabled="busy" @click="doDeposit">
            {{ busy ? '等待钱包确认...' : '确认充值' }}
          </button>
          <p v-if="lastHash" class="text-sm text-mute mt-sm">
            已提交：{{ lastHash.slice(0, 18) }}…（等待确认后到账）
          </p>
        </div>
      </template>
    </div>
  </van-popup>
</template>

<style scoped>
.deposit { padding: 16px; }

.deposit__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.deposit__title { font-size: 17px; font-weight: 700; }
.deposit__close {
  width: 30px; height: 30px; border-radius: 50%;
  background: var(--color-bg-surface-2);
  display: flex; align-items: center; justify-content: center;
  font-size: 16px; color: var(--color-text-mute); border: none; cursor: pointer;
}

.deposit__addr {
  font-size: 13px;
  word-break: break-all;
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-sm);
  padding: 10px;
  margin-top: 6px;
  color: var(--color-primary-strong);
}

.deposit__balance { font-size: 24px; font-weight: 800; margin-top: 4px; }

.deposit__refresh {
  font-size: 12px;
  color: var(--color-primary);
  background: none;
  border: none;
  cursor: pointer;
  padding: 0;
}

.deposit__input {
  width: 100%;
  box-sizing: border-box;
  padding: 12px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-border);
  background: var(--color-bg-surface-2);
  color: var(--color-text);
  font-size: 16px;
}
</style>
