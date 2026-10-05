import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/pages/LoginPage.vue'),
      meta: { public: true, title: '登录' }
    },
    {
      path: '/',
      component: () => import('@/layouts/AppLayout.vue'),
      children: [
        {
          path: '',
          name: 'dashboard',
          component: () => import('@/pages/DashboardPage.vue'),
          meta: { title: '仪表盘', icon: 'dashboard', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'alerts',
          name: 'alerts',
          component: () => import('@/pages/alerts/AlertsPage.vue'),
          meta: { title: '告警中心', icon: 'bell', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'nodes',
          name: 'nodes',
          component: () => import('@/pages/nodes/NodesPage.vue'),
          meta: { title: '设备管理', icon: 'cluster', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'domains',
          name: 'domains',
          component: () => import('@/pages/domains/DomainsPage.vue'),
          meta: { title: '域名与代理', icon: 'global', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'streams',
          name: 'streams',
          component: () => import('@/pages/streams/StreamsPage.vue'),
          meta: { title: '端口转发', icon: 'swap', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'certificates',
          name: 'certificates',
          component: () => import('@/pages/certificates/CertificatesPage.vue'),
          meta: { title: 'TLS 证书', icon: 'safety-certificate', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'pki',
          name: 'pki',
          component: () => import('@/pages/settings/PkiPage.vue'),
          meta: { title: 'PKI / CA', icon: 'lock', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'networks',
          name: 'networks',
          component: () => import('@/pages/networks/NetworksPage.vue'),
          meta: { title: '虚拟网络', icon: 'apartment', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'networks/:id',
          name: 'network-detail',
          component: () => import('@/pages/networks/NetworkDetailPage.vue'),
          meta: { title: '网络详情', hidden: true }
        },
        {
          path: 'users',
          name: 'users',
          component: () => import('@/pages/users/UsersPage.vue'),
          meta: { title: '用户与权限', icon: 'team', roles: ['platform_admin', 'tenant_admin', 'ops'] }
        },
        {
          path: 'logs',
          name: 'logs',
          component: () => import('@/pages/logs/LogsPage.vue'),
          meta: { title: '日志与审计', icon: 'file-search', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'tokens',
          name: 'api-tokens',
          component: () => import('@/pages/tokens/APITokensPage.vue'),
          meta: { title: 'API Token', icon: 'key', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        },
        {
          path: 'settings',
          name: 'settings',
          component: () => import('@/pages/settings/SettingsPage.vue'),
          meta: { title: '系统设置', icon: 'setting', roles: ['platform_admin', 'tenant_admin', 'ops', 'readonly'] }
        }
      ]
    },
    { path: '/:pathMatch(.*)*', redirect: '/' }
  ],
  scrollBehavior: () => ({ top: 0 })
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  document.title = `${String(to.meta.title || 'NEILICO')} · NEILICO 控制台`
  if (!to.meta.public && !auth.isAuthenticated) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  if (to.path === '/login' && auth.isAuthenticated) return '/'
  return true
})

export default router
