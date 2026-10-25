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
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { getUserAvatarFallback, getUserAvatarStyle } from '@/lib/avatar'
import { cn } from '@/lib/utils'

import type { UsageLog } from '../../data/schema'
import { useUsageLogsContext } from '../usage-logs-provider'

export function LogUserCell({
  log,
  interactive,
}: {
  log: UsageLog
  interactive: boolean
}) {
  const { sensitiveVisible, setSelectedUserId, setUserInfoDialogOpen } =
    useUsageLogsContext()

  if (!log.username) return null

  return (
    <button
      type='button'
      className='flex items-center gap-1.5 text-left'
      disabled={!interactive}
      onClick={(e) => {
        e.stopPropagation()
        setSelectedUserId(log.user_id)
        setUserInfoDialogOpen(true)
      }}
    >
      <Avatar className='ring-border/60 size-6 ring-1 max-sm:hidden'>
        <AvatarFallback
          className={cn(
            'text-[11px] font-semibold',
            !sensitiveVisible && 'bg-muted text-muted-foreground'
          )}
          style={
            sensitiveVisible ? getUserAvatarStyle(log.username) : undefined
          }
        >
          {sensitiveVisible ? getUserAvatarFallback(log.username) : '•'}
        </AvatarFallback>
      </Avatar>
      <TooltipProvider delay={300}>
        <Tooltip>
          <TooltipTrigger
            render={
              <span
                className={cn(
                  'text-muted-foreground max-w-[100px] truncate text-sm',
                  interactive && 'hover:underline'
                )}
              />
            }
          >
            {sensitiveVisible ? log.username : '••••'}
          </TooltipTrigger>
          {sensitiveVisible && log.username.length > 12 && (
            <TooltipContent side='top'>{log.username}</TooltipContent>
          )}
        </Tooltip>
      </TooltipProvider>
    </button>
  )
}
