package enum

const USDT = "USDT"

var FUSD = "FUSD"
// FEE_SYMBOL = BOFI：N次方生态众筹参与币（阶段2/3 参与币种之一，见 core/impl/config.go StageSymbols），
// 与手续费币 FeeSymbol 语义独立——勿在手续费链路误用本常量。
var FEE_SYMBOL = "BOFI"
// FeeSymbol = TM：TiMi 手续费币（TiMi 规则第9条：提币 3% 手续费使用 TM 支付）。
// 手续费扣款 / 估价 / 流水 / fee_burn 销毁全链路以本常量为准；币种如需调整仅改此处。
var FeeSymbol = "TM"
var PCB = "PCB"
var FIBO = "FIBO"
var KTO = "KTO"

