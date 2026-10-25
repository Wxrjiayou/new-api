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
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { USER_STATUS } from '@/features/users/constants'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { createServerError } from '@/lib/server-error-message'

import { getAgentUsers } from '../api'
import { isAgentOwnedUserDeleted } from '../lib/user-status'
import type { AgentOwnedUser } from '../types'
import { useAgentUsersColumns } from './agent-users-columns'

const route = getRouteApi('/_authenticated/agent-users/')

function isDisabledRow(user: AgentOwnedUser) {
  return isAgentOwnedUserDeleted(user) || user.status === USER_STATUS.DISABLED
}

export function AgentUsersTable() {
  const { t } = useTranslation()
  const columns = useAgentUsersColumns()
  const isMobile = useMediaQuery('(max-width: 640px)')

  const { pagination, onPaginationChange, ensurePageInRange } =
    useTableUrlState({
      search: route.useSearch(),
      navigate: route.useNavigate(),
      pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    })

  const { data, isLoading, isFetching } = useQuery({
    queryKey: ['agent-users', pagination.pageIndex + 1, pagination.pageSize],
    queryFn: async () => {
      const result = await getAgentUsers({
        p: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
      })
      if (!result.success) {
        throw createServerError(result, t('Failed to load users'))
      }
      return {
        items: result.data?.items || [],
        total: result.data?.total || 0,
      }
    },
    placeholderData: (previousData) => previousData,
  })

  const { table } = useDataTable({
    data: data?.items || [],
    columns,
    pagination,
    onPaginationChange,
    manualPagination: true,
    totalCount: data?.total || 0,
    ensurePageInRange,
  })

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No Users Found')}
      emptyDescription={t('No accounts are assigned to you yet.')}
      skeletonKeyPrefix='agent-users-skeleton'
      applyHeaderSize
      getRowClassName={(row, { isMobile }) => {
        if (!isDisabledRow(row.original)) return undefined
        return isMobile ? DISABLED_ROW_MOBILE : DISABLED_ROW_DESKTOP
      }}
    />
  )
}
