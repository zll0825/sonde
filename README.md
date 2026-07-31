# Capital Observatory

## Documentation

| 文档 | 内容 |
|------|------|
| [PRD v4](docs/prd-v4.md) | 产品定义（v4.1，与架构对齐） |
| [System Architecture](docs/system-architecture.md) | 系统架构（冻结，v1.1） |
| [Domain Model v1.0](docs/domain-model.md) | 领域模型 |
| [Plugin Protocol v1.0](docs/plugin-protocol.md) | gRPC 接口定义 |
| [Database Schema v1.0](docs/database-schema.md) | 数据库 Schema |
| [ADR](docs/adr.md) | 架构决策记录（ADR-1…7 + M1 待决事项） |
| [Conventions](docs/conventions.md) | Go 代码约定 |

## Getting Started

```bash
# 1. Start database
docker compose -f deployments/docker-compose.yml up db

# 2. Install tools
go install github.com/bufbuild/buf/cmd/buf@latest
go install github.com/cosmtrek/air@latest

# 3. Generate proto
buf generate

# 4. Run Core
cd cmd/core && air

# 5. Run API Server
cd cmd/api && air

# 6. Run a Plugin (e.g. ETF)
cd plugins/etf && go run cmd/main.go --core-addr=localhost:9090
```

## Project Status

- [x] Architecture frozen（v1.1：里程碑修订，设计不变）
- [x] Docs aligned：PRD v4.1 ↔ Architecture（ADR-7 词表映射、M1 待决事项）
- [ ] M0: Repository Bootstrap（compose / migrations / buf generate / CI）
- [ ] M1: Plugin Registration Path
