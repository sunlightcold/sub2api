ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS long_context_pricing_enabled BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN groups.long_context_pricing_enabled IS
    '是否启用模型定义的长上下文阶梯计费；关闭时始终使用当前服务档位的基础 token 单价';
