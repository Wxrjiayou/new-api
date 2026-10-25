/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { ActivityTimeCell } from '@/components/activity-time-cell'
import { BadgeCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { LongText } from '@/components/long-text'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { UserQuotaCell } from '@/features/users/components/user-quota-cell'
import { USER_STATUS, USER_STATUSES } from '@/features/users/constants'
import { getCurrencyDisplay } from '@/lib/currency'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { isAgentOwnedUserDeleted } from '../lib/user-status'
import type { AgentOwnedUser } from '../types'
import { AgentUserActionsCell } from './agent-user-actions-cell'

export function useAgentUsersColumns(): ColumnDef<AgentOwnedUser>[] {
  const { t } = useTranslation()
  const currencyConfig = useSystemConfigStore((state) => state.config.currency)
  const selfId = useAuthStore((state) => state.auth.user?.id)
  const { meta: currency } = getCurrencyDisplay()
  const quotaUnit = currency.kind === 'tokens' ? t('Tokens') : currency.symbol
  return useMemo<ColumnDef<AgentOwnedUser>[]>(
    () => [
      {
        accessorKey: 'id',
        header: t('ID'),
        cell: ({ row }) => (
          <TableId
            value={row.original.id}
            className='w-[60px] [font-family:inherit] text-sm'
          />
        ),
        size: 80,
        meta: { mobileOrder: 10 },
      },
      {
        accessorKey: 'username',
        header: t('Username'),
        cell: ({ row }) => {
          const { username, display_name: displayName, remark } = row.original
          return (
            <div className='flex min-w-[160px] flex-col gap-1'>
              <div className='flex items-center gap-2'>
                <LongText className='max-w-[140px] text-sm font-normal'>
                  {username}
                </LongText>
                {row.original.id === selfId && (
                  <StatusBadge
                    label={t('My account')}
                    variant='info'
                    copyable={false}
                    className='font-normal'
                  />
                )}
                {remark && (
                  <StatusBadge
                    variant='success'
                    copyable={false}
                    className='font-normal'
                  >
                    <LongText className='max-w-[80px]'>{remark}</LongText>
                  </StatusBadge>
                )}
              </div>
              {displayName && displayName !== username && (
                <div
                  data-table-text='secondary'
                  className='text-muted-foreground max-w-[180px] text-xs font-normal'
                >
                  <LongText>{displayName}</LongText>
                </div>
              )}
            </div>
          )
        },
        enableHiding: false,
        size: 220,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const user = row.original
          const deleted = isAgentOwnedUserDeleted(user)
          const statusConfig = deleted
            ? USER_STATUSES[USER_STATUS.DELETED]
            : USER_STATUSES[user.status as keyof typeof USER_STATUSES]
          if (!statusConfig) return null
          return (
            <StatusBadge
              label={t(statusConfig.labelKey)}
              variant={deleted ? 'neutral' : statusConfig.variant}
              copyable={false}
              className='font-normal'
            />
          )
        },
        enableSorting: false,
        size: 120,
        meta: { mobileBadge: true },
      },
      {
        id: 'quota',
        accessorKey: 'quota',
        header: `${t('Available Balance')} (${quotaUnit})`,
        cell: ({ row }) => (
          <UserQuotaCell
            remaining={row.original.quota}
            used={row.original.used_quota}
          />
        ),
        size: 180,
        minSize: 160,
        meta: { mobileOrder: 40 },
      },
      {
        accessorKey: 'request_count',
        header: t('Requests'),
        cell: ({ row }) => (
          <span className='text-sm tabular-nums'>
            {row.original.request_count.toLocaleString()}
          </span>
        ),
        size: 110,
        meta: { mobileOrder: 50 },
      },
      {
        accessorKey: 'group',
        header: t('User Group'),
        cell: ({ row }) => (
          <BadgeCell>
            <GroupBadge group={row.original.group} className='font-normal' />
          </BadgeCell>
        ),
        size: 140,
        meta: { mobileOrder: 30 },
      },
      {
        accessorKey: 'created_at',
        header: t('Time'),
        cell: ({ row }) => (
          <ActivityTimeCell
            createdAt={row.original.created_at ?? 0}
            lastAt={row.original.last_login_at ?? 0}
            lastLabel={t('Last Login')}
            format='absolute'
          />
        ),
        size: 260,
        minSize: 240,
        meta: { mobileHidden: true },
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: () => <AgentUserActionsCell />,
        meta: { pinned: 'right' as const },
      },
    ],
    // UserQuotaCell reads the currency configuration from the store.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, quotaUnit, currencyConfig, selfId]
  )
}
