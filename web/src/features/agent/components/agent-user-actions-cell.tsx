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
import { MoreHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'

// Agents never manage the accounts they own, so the actions cell explains
// that instead of opening a menu.
export function AgentUserActionsCell() {
  const { t } = useTranslation()
  return (
    <Button
      variant='ghost'
      size='icon'
      aria-disabled='true'
      aria-label={t('Agents cannot manage users')}
      className='opacity-50'
      onClick={() => toast.info(t('Agents cannot manage users'))}
    >
      <MoreHorizontal aria-hidden='true' />
    </Button>
  )
}
