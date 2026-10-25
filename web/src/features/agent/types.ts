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

/**
 * The part of an owned account an agent may see. Mirrors the backend's
 * AgentOwnedUser: what reconciling usage needs, none of the bindings.
 */
export interface AgentOwnedUser {
  id: number
  username: string
  display_name: string
  remark: string
  group: string
  status: number
  quota: number
  used_quota: number
  request_count: number
  created_at: number
  last_login_at: number
  DeletedAt: string | null
}

export interface GetAgentUsersParams {
  p?: number
  page_size?: number
}

export interface GetAgentUsersResponse {
  success: boolean
  message?: string
  data?: {
    items: AgentOwnedUser[]
    total: number
    page: number
    page_size: number
  }
}
