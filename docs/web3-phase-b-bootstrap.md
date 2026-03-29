# Web3 Phase B Bootstrap (BET ERC-20)

Файл фиксирует минимальный bootstrap для старта `Phase B` из `docs/web3-implementation-tracker.md`.

## Что добавлено в репозитории

- Референс-контракт: `backend/docs/bet-erc20-reference.sol`
- Шаблон sign-off фазы A: `docs/web3-phase-a-signoff-checklist.md`

## Рекомендуемая структура каталога контрактов (следующий коммит)

```text
backend/web3/contracts/
  BETToken.sol
backend/web3/script/
  DeployBET.s.sol (или deploy.ts)
backend/web3/out/
  BETToken.abi.json
```

## Минимальный конфиг для деплоя в тестнет

```env
BET_DEPLOYER_PRIVATE_KEY=...
BET_ADMIN_ADDRESS=0x...
BET_MINTER_ADDRESS=0x...
BET_PAUSER_ADDRESS=0x...
BET_INITIAL_SUPPLY_WEI=1000000000000000000000000
BET_RPC_URL=https://...
```

## Артефакты после деплоя (обязательные)

1. `chain_id`
2. `bet_token_address`
3. `bet_token_abi` (json файл)

Эти три значения передаются в Phase C (индексатор и backend-конфиг).
