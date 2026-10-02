<script setup lang="ts">
import { SafetyCertificateOutlined } from '@ant-design/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import PKIManagement from '@/components/PKIManagement.vue'
</script>

<template>
  <div class="page-container pki-page">
    <PageHeader
      title="PKI / CA 管理"
      subtitle="内部 CA 信任材料与节点 mTLS 客户端证书生命周期"
    >
      <template #actions>
        <a-tag color="blue"><SafetyCertificateOutlined /> 私钥永不显示</a-tag>
      </template>
    </PageHeader>
    <PKIManagement />
  </div>
</template>
