// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.di

import android.content.Context
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import java.util.concurrent.TimeUnit
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import okhttp3.OkHttpClient
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.KeystoreSecretBox
import org.simpleapb.core.data.account.accountsDataStore
import org.simpleapb.core.data.auth.AuthRepository
import org.simpleapb.core.data.net.AccountCalls
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.data.net.AppScope

/** Data-layer singletons. The app module provides [AppInfo]. */
@Module
@InstallIn(SingletonComponent::class)
object DataModule {
    @Provides
    @Singleton
    fun accountStore(@ApplicationContext context: Context): AccountStore = AccountStore(context.accountsDataStore, KeystoreSecretBox())

    @Provides
    @Singleton
    fun okHttp(): OkHttpClient = OkHttpClient
        .Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    @Provides
    @Singleton
    fun apiFactory(http: OkHttpClient, store: AccountStore, app: AppInfo): ApiFactory = ApiFactory(http, store, app)

    @Provides
    @Singleton
    fun authRepository(apis: ApiFactory, store: AccountStore, app: AppInfo): AuthRepository = AuthRepository(apis, store, app)

    @Provides
    @Singleton
    @AppScope
    fun appScope(): CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    @Provides
    @Singleton
    fun accountCalls(store: AccountStore, apis: ApiFactory, http: OkHttpClient, @AppScope scope: CoroutineScope): AccountCalls =
        AccountCalls(store, apis, http, scope)
}
